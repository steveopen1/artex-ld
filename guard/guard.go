// guard.go：安全边界层——审计日志、拦截规则评估（PreToolUse 钩子）与失败归因。
// 主要函数/类型：
//   - AuditEntry / Guard：审计条目与安全守卫主体。
//   - New / NewWithInterceptor / newGuard：构造 Guard。
//   - Hooks / preToolUse / applyIntercept / postToolUse：钩子注册与工具调用前中后拦截。
//   - Attributions / block / record / Audit：失败归因、阻断与审计查询。
//   - interceptorAPI / systemBlockMessage：拦截器抽象与系统阻断文案。
//
// Package guard implements the safety boundary layer (docs §11): an audit log,
// user-configured intercept-rule evaluation, and Observer/G5 failure attribution.
// Every tool call passes through the PreToolUse hook before executing.
// (The RoE authorization-scope mechanism was removed; a replacement may be added
// later.) Destructive/exfil gating is no longer hard-coded here — it lives in the
// DB intercept rules (seeded as ordinary [内置] rules, so users can disable or
// delete them), evaluated via applyIntercept.
package guard

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pgdb "artex/db"
	"artex/intercept"
	"norma/hook"
)

// AuditEntry records one gated tool call.
type AuditEntry struct {
	TS      int64  `json:"ts"`
	Tool    string `json:"tool"`
	Action  string `json:"action"` // allow|block
	Reason  string `json:"reason,omitempty"`
	Command string `json:"command,omitempty"`
}

// interceptorAPI 是 Guard 使用的审批能力；接口让审计逻辑可以独立做单元测试。
type interceptorAPI interface {
	IsToolEnabled(string) bool
	Match(string, []byte) (intercept.Decision, bool)
	Judge(context.Context, string, json.RawMessage) (intercept.Decision, bool)
	Log(context.Context, int64, intercept.Decision, string, []byte, string)
	HandleAsk(context.Context, int64, intercept.Decision, string, []byte) bool
}

// Guard enforces the side-effect policy via agent-core hooks.
type Guard struct {
	mu     sync.Mutex
	audit  []AuditEntry
	attrib map[string]int // failure attribution counts (Observer / G5)
	// decisions counts platform-governance outcomes per tool and action:
	// "deny:<tool>", "ask_allowed:<tool>", "ask_denied:<tool>", "allow:<tool>".
	// These are fed to the planner via graph_overview so it can see that a
	// direction is being blocked instead of re-trying it every round.
	decisions   map[string]int
	targetBlock map[string]int // target-side WAF/403 hits per tool (from postToolUse)
	// msgFn returns the operator-configured template for "blocked" messages.
	// nil = DefaultBlockMessageTemplate (the historical wording). Read per call so
	// a settings change takes effect without rebuilding the task's Guard.
	msgFn func() string
	// trafficFn looks up the recorded MITM exchanges that match a blocked shell
	// command, so the reminder can carry real request/response evidence instead of
	// just the command text. nil = no traffic evidence (feature off / no store).
	trafficFn   func(ctx context.Context, tool, command string) []BlockedTraffic
	// scopeFn 是任务范围校验钩子：给定工具名与命令文本，返回 (是否拦截, 原因)。
	// 为什么放在 guard 而不是 agent 层：guard 已经挂在每个 worker/chat 的
	// PreToolUse 上，是唯一能看到"所有工具调用 + 完整命令文本"的地方；
	// 而 ScopeMatcher 需要 taskID 与 DB，只有 server 层有 —— 所以这里只留
	// 一个函数钩子，由 server 注入（与 trafficFn 完全同构）。
	// nil = 不做范围校验（未配置 / chat 场景）。
	scopeFn func(ctx context.Context, tool, command string) (block bool, reason string)
	reg         *hook.Registry
	interceptor interceptorAPI // optional; nil disables user-configured rules
}

// New creates a Guard without user-configured intercept rules (used for pentest
// tasks where the Interceptor is not yet available).
func New() *Guard { return newGuard(nil) }

// NewWithInterceptor creates a Guard with user-configured intercept rules.
func NewWithInterceptor(ic *intercept.Interceptor) *Guard {
	if ic == nil {
		return newGuard(nil)
	}
	return newGuard(ic)
}

func newGuard(ic interceptorAPI) *Guard {
	g := &Guard{
		attrib:      map[string]int{},
		decisions:   map[string]int{},
		targetBlock: map[string]int{},
		interceptor: ic,
	}
	g.reg = hook.NewRegistry().
		On(hook.PreToolUse, g.preToolUse).
		On(hook.PostToolUse, g.postToolUse)
	return g
}

// Hooks returns the hook registry to attach to an agent session.
func (g *Guard) Hooks() *hook.Registry { return g.reg }

func (g *Guard) preToolUse(ctx context.Context, ev hook.Event) hook.Result {
	// Extract the shell-command surface for the audit log: Bash + the interactive-shell
	// tools (shell_open's command, shell_send's text). Destructive/exfil gating is no
	// longer hard-coded here — it now lives in the DB intercept rules, evaluated by
	// applyIntercept below. Other tools record an empty command.
	var cmd string
	switch ev.ToolName {
	case "Bash", "shell_open":
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(ev.Input, &in)
		cmd = in.Command
	case "shell_send":
		var in struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(ev.Input, &in)
		cmd = in.Text
	}
	result := g.applyIntercept(ctx, ev, cmd)
	if result.Decision == "block" {
		return result
	}
	// 范围校验在拦截规则之后：规则是"这个动作本身被禁"，范围是"这个目标被禁"。
	// 两者都过才放行。只对携带命令文本的工具有效（Bash/shell_*）—— 其余工具
	// 的目标可从结构化参数里拿到，由各自工具内部自查（见 customtool 与 run_tool）。
	if blocked, reason := g.checkScope(ctx, ev.ToolName, cmd); blocked {
		g.record(ev.ToolName, "deny", "范围外目标", cmd)
		return hook.Result{Decision: "block", Message: reason}
	}
	g.record(ev.ToolName, "allow", "", cmd)
	return result
}

// checkScope 调用注入的范围校验器；未注入或命令为空时直接放行。
func (g *Guard) checkScope(ctx context.Context, tool, cmd string) (bool, string) {
	g.mu.Lock()
	fn := g.scopeFn
	g.mu.Unlock()
	if fn == nil || strings.TrimSpace(cmd) == "" {
		return false, ""
	}
	return fn(ctx, tool, cmd)
}

// applyIntercept evaluates user-configured intercept rules against the tool call.
// Both rules and the fallback judge receive the complete tool input.
func (g *Guard) applyIntercept(ctx context.Context, ev hook.Event, cmd string) hook.Result {
	if g.interceptor == nil {
		return hook.Result{}
	}
	// 【二开·审核流 PR1/§2.8】K15 审核写工具闸门来源校验：非 review 会话（如 planner
	// 调 add_review_intent 送审）直接绕过，不进规则匹配/LLM 判官——否则 planner 每次
	// 送审都会弹人工确认卡（种子规则把这三件工具加进了全局启用集）。
	if intercept.SkipReviewGateFor(ctx, ev.ToolName) {
		return hook.Result{}
	}
	if !g.interceptor.IsToolEnabled(ev.ToolName) {
		return hook.Result{}
	}
	ctx = intercept.WithCall(ctx, ev.ToolName, ev.Input)
	dec, matched := g.interceptor.Match(ev.ToolName, ev.Input)
	if !matched {
		// No rule matched. Ask the LLM fallback judge (if enabled); when it is off
		// or unwired, keep current behavior and allow.
		d, judged := g.interceptor.Judge(ctx, ev.ToolName, ev.Input)
		if !judged {
			return hook.Result{}
		}
		dec = d
	}
	switch dec.Action {
	case "deny":
		// 观测:deny 命中不阻塞审批,直接记一条 denied（历史/任务拦截页可见）。
		g.interceptor.Log(ctx, intercept.ConvIDFromContext(ctx), dec, ev.ToolName, ev.Input, "denied")
		g.count("deny:" + ev.ToolName)
		msg := g.blockMessage(ctx, ev, dec, "deny", cmd)
		// 与会话流对齐：ask 会写一张审批卡，deny 过去只落库，会话里看不到「刚被拦了」。
		// 写一条提醒进活动流，前端才能把它显示在对应 worker 的对话里。
		g.emitBlocked(ctx, ev, dec, "deny", cmd, msg)
		return g.block(ev.ToolName, msg, cmd)
	case "allow":
		// Record explicit rule and model approvals so review details remain auditable.
		g.interceptor.Log(ctx, intercept.ConvIDFromContext(ctx), dec, ev.ToolName, ev.Input, "allowed")
		g.count("allow:" + ev.ToolName)
		return hook.Result{}
	case "ask":
		// If the worker context is already cancelled (task stopped / killed), block
		// immediately without creating a pending record — avoids orphaned DB entries
		// and makes execOne complete fast, reducing the race against drainSynthetic.
		if ctx.Err() != nil {
			g.count("ask_denied:" + ev.ToolName)
			msg := g.blockMessage(ctx, ev, dec, "cancelled", cmd)
			g.emitBlocked(ctx, ev, dec, "cancelled", cmd, msg)
			return g.block(ev.ToolName, msg, cmd)
		}
		convID := intercept.ConvIDFromContext(ctx)
		if !g.interceptor.HandleAsk(ctx, convID, dec, ev.ToolName, ev.Input) {
			g.count("ask_denied:" + ev.ToolName)
			msg := g.blockMessage(ctx, ev, dec, "ask_denied", cmd)
			g.emitBlocked(ctx, ev, dec, "ask_denied", cmd, msg)
			return g.block(ev.ToolName, msg, cmd)
		}
		g.count("ask_allowed:" + ev.ToolName)
		return hook.Result{}
	}
	return hook.Result{}
}

// systemBlockMessage frames an intercept block as an ARTEX platform-governance
// decision so the agent does not mistake it for a target-side defense.
//
// The bare reasons ("禁止执行此工具" / "用户拒绝") read exactly like a WAF/403 on
// the target, so a pentest agent's instinct is to bypass them — rewrite the
// command, swap the payload, re-encode, retry. That is both futile (the platform
// blocks the class of action, not one string) and wrong (it's a policy decision,
// not an obstacle to defeat). This prefix states plainly that the block comes
// from the platform, is not the target's protection, and that the operation is
// forbidden — so the agent pivots to another approach instead of evading it.
// Audit/history rows keep the raw reason (see Interceptor.Log); only the
// model-facing tool_result carries this framing.
func systemBlockMessage(reason string) string {
	return RenderBlockMessage("", BlockMessageVars{Reason: reason})
}

var reBlocked = regexp.MustCompile(`(?i)\b(403|forbidden|waf|blocked|rate.?limit|429|captcha|denied)\b`)

// BlockedTraffic is one recorded MITM exchange attached to a block reminder.
// Deliberately small: id + method/path/status is enough for the model to know
// "this is the request that was about to run", and the model can pull the body
// with traffic_get if it needs more.
type BlockedTraffic struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	URL    string `json:"url"`
	Status int    `json:"status"`
}

// SetBlockMessageTemplate wires the operator-configured block message template
// (settings.intercept_block_message). nil keeps the built-in default.
func (g *Guard) SetBlockMessageTemplate(fn func() string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.msgFn = fn
	g.mu.Unlock()
}

// SetTrafficMatcher wires the recorded-traffic lookup used to attach evidence to
// a block reminder. nil = no evidence (feature off or no traffic store).
func (g *Guard) SetTrafficMatcher(fn func(ctx context.Context, tool, command string) []BlockedTraffic) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.trafficFn = fn
	g.mu.Unlock()
}

// SetScopeChecker 注入任务范围校验钩子。与 SetTrafficMatcher 同构：
// guard 不持有 taskID，也不 import server，只调用注入进来的判定函数。
func (g *Guard) SetScopeChecker(fn func(ctx context.Context, tool, command string) (bool, string)) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.scopeFn = fn
	g.mu.Unlock()
}

// blockMessage renders the model-facing block message: operator template (if any),
// with tool / rule / reason / command variables expanded.
func (g *Guard) blockMessage(ctx context.Context, ev hook.Event, dec intercept.Decision, action, cmd string) string {
	var tpl string
	var matcher func(context.Context, string, string) []BlockedTraffic
	g.mu.Lock()
	if g.msgFn != nil {
		tpl = g.msgFn()
	}
	matcher = g.trafficFn
	g.mu.Unlock()

	reason := dec.Message
	if action == "cancelled" {
		reason = "工作已取消，平台安全管控阻止执行"
	} else if action == "ask_denied" && reason == "" {
		reason = "人工审批未通过（用户拒绝或审批超时）"
	}
	msg := RenderBlockMessage(tpl, BlockMessageVars{
		Tool:    ev.ToolName,
		Rule:    dec.RuleName,
		Reason:  reason,
		Command: cmd,
		Action:  action,
	})
	// Attach real traffic evidence when the blocked command maps to recorded
	// exchanges. Appended as a separate paragraph so a custom template that omits
	// {command} still gets the evidence, and one that includes it is not doubled.
	if matcher != nil {
		if ex := matcher(ctx, ev.ToolName, cmd); len(ex) > 0 {
			msg += "\n\n【对应流量证据】被拦命令相关的真实报文：" + formatBlockedTraffic(ex)
		}
	}
	return msg
}

// formatBlockedTraffic renders at most 3 exchanges inline. More than that is
// noise: the reminder is about "don't do this", the ids are the useful part.
func formatBlockedTraffic(ex []BlockedTraffic) string {
	const maxInline = 3
	n := len(ex)
	if n > maxInline {
		n = maxInline
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		e := ex[i]
		if i > 0 {
			b.WriteString("；")
		}
		b.WriteString(e.Method)
		b.WriteString(" ")
		b.WriteString(e.URL)
		b.WriteString(" → ")
		b.WriteString(strconv.Itoa(e.Status))
		b.WriteString(" (traffic_id=")
		b.WriteString(e.ID)
		b.WriteString(")")
	}
	if len(ex) > n {
		b.WriteString("；另有 ")
		b.WriteString(strconv.Itoa(len(ex) - n))
		b.WriteString(" 条，可用 traffic_search 查看")
	}
	return b.String()
}

// emitBlocked writes a task/conversation activity so the block shows up inline in
// the worker's session, matching what the ask path already does. Without this the
// only trace of a deny is the audit table, which the model never sees.
func (g *Guard) emitBlocked(ctx context.Context, ev hook.Event, dec intercept.Decision, action, cmd, msg string) {
	emit := intercept.TaskEmitFromContext(ctx)
	convID := intercept.ConvIDFromContext(ctx)
	if emit == nil && convID == 0 {
		return
	}
	detail := map[string]any{
		"tool":    ev.ToolName,
		"action":  action,
		"rule":    dec.RuleName,
		"reason":  dec.Message,
		"command": escapeForLog(cmd),
		"message": msg,
	}
	raw, _ := json.Marshal(detail)
	act := pgdb.Activity{
		Kind:    "intercept_denied",
		Summary: "平台拦截 " + ev.ToolName + "（" + actionLabelCN(action) + "）",
		Detail:  string(raw),
	}
	if convID != 0 {
		// Chat session: same append path the ask card uses.
		_ = convID
	}
	if emit != nil {
		emit(act)
	}
}

// actionLabelCN maps the internal action codes to the words operators use in the
// approvals UI, so the activity line reads the same as the audit page.
func actionLabelCN(action string) string {
	switch action {
	case "deny":
		return "规则拒绝"
	case "ask_denied":
		return "审批未通过"
	case "cancelled":
		return "工作已取消"
	default:
		return action
	}
}

// postToolUse is the Observer failure-attribution hook (G5): it classifies tool
// results into blocked / error / ok so the planner can change strategy instead
// of giving up at a WAF.
func (g *Guard) postToolUse(_ context.Context, ev hook.Event) hook.Result {
	if ev.ToolName != "Bash" {
		return hook.Result{}
	}
	class := "ok"
	switch {
	case reBlocked.Match(ev.Result):
		class = "blocked"
	case ev.IsError:
		class = "error"
	}
	g.mu.Lock()
	g.attrib[class]++
	if class == "blocked" {
		g.targetBlock[ev.ToolName]++
	}
	g.mu.Unlock()
	return hook.Result{}
}

// count increments one decision counter. Internal to the guard's mutex; callers
// must not hold g.mu.
func (g *Guard) count(key string) {
	g.mu.Lock()
	if g.decisions == nil {
		g.decisions = map[string]int{}
	}
	g.decisions[key]++
	g.mu.Unlock()
}

// GuardStats is the planner-facing summary of platform governance activity for
// one task. Counts are cumulative for the task's lifetime (the same guard object
// lives as long as the task).
type GuardStats struct {
	// Denied / AskDenied / AskAllowed / Allowed are totals across all tools.
	Denied     int `json:"denied"`
	AskDenied  int `json:"ask_denied"`
	AskAllowed int `json:"ask_allowed"`
	Allowed    int `json:"allowed"`
	// TargetBlocked counts tool results that looked like a target-side WAF/403/429.
	TargetBlocked int `json:"target_blocked"`
	// ByTool is a compact per-tool breakdown, largest first, capped at 10 entries.
	ByTool []ToolDecision `json:"by_tool,omitempty"`
}

// ToolDecision is one row of the per-tool breakdown.
type ToolDecision struct {
	Tool       string `json:"tool"`
	Denied     int    `json:"denied,omitempty"`
	AskDenied  int    `json:"ask_denied,omitempty"`
	AskAllowed int    `json:"ask_allowed,omitempty"`
	Allowed    int    `json:"allowed,omitempty"`
	// TargetBlocked is the target-side WAF/403 count for this tool.
	TargetBlocked int `json:"target_blocked,omitempty"`
}

// Stats returns the task's governance summary. A nil Guard returns a zero value
// so callers need no nil check.
func (g *Guard) Stats() GuardStats {
	var out GuardStats
	if g == nil {
		return out
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	byTool := map[string]*ToolDecision{}
	row := func(tool string) *ToolDecision {
		d, ok := byTool[tool]
		if !ok {
			d = &ToolDecision{Tool: tool}
			byTool[tool] = d
		}
		return d
	}
	for key, n := range g.decisions {
		action, tool, ok := cutOnce(key, ":")
		if !ok {
			continue
		}
		switch action {
		case "deny":
			out.Denied += n
			row(tool).Denied += n
		case "ask_denied":
			out.AskDenied += n
			row(tool).AskDenied += n
		case "ask_allowed":
			out.AskAllowed += n
			row(tool).AskAllowed += n
		case "allow":
			out.Allowed += n
			row(tool).Allowed += n
		}
	}
	for tool, n := range g.targetBlock {
		out.TargetBlocked += n
		row(tool).TargetBlocked += n
	}
	for _, d := range byTool {
		out.ByTool = append(out.ByTool, *d)
	}
	sort.Slice(out.ByTool, func(i, j int) bool {
		a, b := out.ByTool[i], out.ByTool[j]
		sa := a.Denied + a.AskDenied + a.AskAllowed + a.TargetBlocked
		sb := b.Denied + b.AskDenied + b.AskAllowed + b.TargetBlocked
		if sa != sb {
			return sa > sb
		}
		return a.Tool < b.Tool
	})
	if len(out.ByTool) > 10 {
		out.ByTool = out.ByTool[:10]
	}
	return out
}

// cutOnce splits s at the first occurrence of sep.
func cutOnce(s, sep string) (before, after string, ok bool) {
	i := strings.Index(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// Attributions returns failure-attribution counts (Observer / G5).
func (g *Guard) Attributions() map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]int, len(g.attrib))
	for k, v := range g.attrib {
		out[k] = v
	}
	return out
}

func (g *Guard) block(tool, reason, cmd string) hook.Result {
	g.record(tool, "block", reason, cmd)
	return hook.Result{Decision: "block", Message: reason}
}

func (g *Guard) record(tool, action, reason, cmd string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.audit = append(g.audit, AuditEntry{TS: time.Now().Unix(), Tool: tool, Action: action, Reason: reason, Command: cmd})
	if len(g.audit) > 2000 {
		g.audit = g.audit[len(g.audit)-2000:]
	}
}

// Audit returns a snapshot of recent gated calls (most recent last).
func (g *Guard) Audit() []AuditEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]AuditEntry, len(g.audit))
	copy(out, g.audit)
	return out
}

// RecordForTest increments one decision counter. Exported only so other packages'
// tests can exercise the plumbing without an interceptor; production code paths
// go through applyIntercept / postToolUse.
func (g *Guard) RecordForTest(action, tool string) {
	if g == nil {
		return
	}
	g.count(action + ":" + tool)
}
