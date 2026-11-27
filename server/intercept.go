// intercept.go：内容拦截规则的 HTTP 管理接口与聊天完成内容复核接入。
// 主要函数/类型：
//   - chatGuard / wireInterceptReviewer：构建拦截 Guard 并把 LLM 完成内容复核器接入聊天流程。
//   - interceptListRules / interceptCreateRule / interceptUpdateRule / interceptDeleteRule / interceptToggleRule：拦截规则的增删改查与启停接口。
//   - interceptListPending / interceptGetOne / interceptDetail / interceptDecide：待审项列表、详情与人工裁决接口。
//   - interceptGetToolConfig / interceptSetToolConfig / interceptGetJudgeConfig / interceptSetJudgeConfig：拦截工具与评审模型的配置读写。
//   - reviewCompletion / streamCollectText：调用 LLM 复核输入内容并收集流式文本。
package server

import (
	"artex/db"
	"artex/guard"
	"artex/intercept"
	"artex/intercept/jev"
	"artex/llmrec"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"norma/llm"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// judgeWorkerLane is the usage-ledger "worker" label for intercept fallback-judge
// calls, so judge spend can be queried apart from the worker/planner/main lanes.
const judgeWorkerLane = "judge"

// chatGuard returns a guard wired with the manager's interceptor, used for chat
// conversations. Called once per applyLLM so a new LLM config always gets a fresh guard.
func (s *Server) chatGuard() *guard.Guard {
	return guard.NewWithInterceptor(s.m.interceptor)
}

// resolveJudgeProfileID 把「裁判模型」的配置解析成一个真实可用的 profile id。
//
// 入参是 llm_judge_profile_id 的设置值，0 表示「跟随全局默认」。这里必须把 0
// 展开成具体的 id，因为下游 ProfileByID(0) 恒查无此行。
//
// 解析顺序：显式配置 → is_default → LLM 池。
//
// 最后一级池兜底不是防御性冗余，而是修掉一个真实故障模式：库里完全可以只有
// 优先级档、没有任何 is_default（实测 llm_profiles 有 2 条、is_default=0 条时
// 就是这种状态，测试污染也会造成它）。此时 ActiveProfile() 返回 nil，若无兜底
// 就直接报「未配置可用的裁判模型」，于是**所有工具审批全线失败** —— 而池里明明
// 躺着可用模型。这与 reviewProvider 的池兜底是同一类修复，两处必须保持一致。
//
// 注意 jev 档位：它在池里是合法的裁判模型（SetActiveProfile 只是禁止把它设为
// 全局默认），所以池兜底命中 jev 是允许的，由调用方按 Format 走 jev 分支。
func (s *Server) resolveJudgeProfileID(configured int64) int64 {
	if configured > 0 {
		return configured
	}
	if p, err := s.m.pg.ActiveProfile(); err == nil && p != nil {
		return p.ID
	} else if err != nil {
		log.Printf("[intercept] 读取全局默认 LLM 配置失败: %v", err)
	}
	pool, err := s.m.pg.PoolProfiles()
	if err != nil {
		log.Printf("[intercept] 裁判模型无 is_default 且读取 LLM 池失败: %v", err)
		return 0
	}
	for _, p := range pool {
		log.Printf("[intercept] 裁判模型回落到 LLM 池档位 id=%d（%s）：未配置裁判模型且无 is_default", p.ID, p.Name)
		return p.ID
	}
	log.Printf("[intercept] 无可用裁判模型：未配置 llm_judge_profile_id、无 is_default、池内 %d 条档位均不可用", len(pool))
	return 0
}

// wireInterceptReviewer installs the LLM fallback judge into the interceptor. The
// judge runs only on tool calls that matched no rule (see intercept.Judge). It
// resolves the configured judge profile (0 → active/default), builds a provider,
// runs a one-shot JSON classification with an explanation for every verdict.
func (s *Server) wireInterceptReviewer() {
	s.m.interceptor.SetReviewer(func(ctx context.Context, profileID int64, prompt string, input intercept.ReviewInput) (intercept.Decision, error) {
		profileID = s.resolveJudgeProfileID(profileID)
		if profileID == 0 {
			return intercept.Decision{}, fmt.Errorf("未配置可用的裁判模型")
		}
		profile, err := s.m.pg.ProfileByID(profileID)
		if err != nil || profile == nil {
			return intercept.Decision{ProfileID: profileID}, fmt.Errorf("裁判模型 profile %d 不可用", profileID)
		}
		if profile.Format == "jev" {
			if strings.TrimSpace(profile.APIKey) == "" {
				return intercept.Decision{ProfileID: profileID}, fmt.Errorf("Jev 判定模型未设置 API Key")
			}
			ctx = llmrec.WithWorker(ctx, judgeWorkerLane)
			started := time.Now()
			decision, usage, reviewErr := (jev.Config{
				BaseURL: profile.BaseURL, APIKey: profile.APIKey, Model: profile.Model, Proxy: profile.Proxy,
			}).ReviewWithUsage(ctx, input, prompt, s.m.interceptor.GetJudgeConfig().ConfidenceThreshold, profileID)
			s.recordJevJudgeUsage(ctx, profile, usage, started, reviewErr)
			return decision, reviewErr
		}
		prov, _, ok := s.providerForProfile(profileID)
		if !ok {
			return intercept.Decision{ProfileID: profileID}, fmt.Errorf("裁判模型 profile %d 不可用", profileID)
		}
		// Tag this call's usage as the "judge" lane so the config page can report
		// how much the fallback approval has spent, separate from model profiles.
		ctx = llmrec.WithWorker(ctx, judgeWorkerLane)
		// The judge follows the profile's own thinking setting: a model that
		// requires adaptive thinking rejects thinking=disabled outright.
		text, err := reviewCompletion(ctx, prov, prompt, input, profile.ThinkingType)
		if err != nil {
			return intercept.Decision{ProfileID: profileID}, err
		}
		v := intercept.ParseVerdict(text)
		if v.Action == "" {
			return intercept.Decision{ProfileID: profileID}, fmt.Errorf("模型裁决格式无效，必须包含裁决、实际操作、成功后的后果和命中规则")
		}
		return intercept.Decision{Action: v.Action, Message: v.Reason, ProfileID: profileID}, nil
	})
}

func (s *Server) recordJevJudgeUsage(ctx context.Context, profile *db.LLMProfile, usage jev.Usage, started time.Time, callErr error) {
	if s == nil || s.m == nil || s.m.PG() == nil || profile == nil {
		return
	}
	status := "ok"
	if callErr != nil {
		status = "error"
	}
	model := strings.TrimSpace(profile.Model)
	if model == "" {
		model = jev.DefaultModel
	}
	if err := s.m.PG().InsertLLMUsage(&db.LLMUsage{
		TaskID:       llmrec.TaskIDFrom(ctx),
		Worker:       judgeWorkerLane,
		Model:        model,
		ProfileName:  profile.Name,
		LatencyMs:    int(time.Since(started).Milliseconds()),
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		Status:       status,
	}); err != nil {
		log.Printf("[llmusage] Jev judge insert: %v", err)
	}
}

func reviewCompletion(ctx context.Context, prov llm.Provider, prompt string, input intercept.ReviewInput, thinking string) (string, error) {
	user, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	return streamCollectTextWithThinking(ctx, prov, prompt, string(user), thinking)
}

// streamCollectText runs a single non-streaming-style completion (low
// temperature, bounded output) and returns the concatenated text. The budget
// includes the explanation and complete closing JSON delimiters.
//
// Thinking is NOT hardcoded off. It used to be, because a verdict should be
// fast — but some models (MiniMax-M3.1-Flash and friends) REQUIRE adaptive
// thinking and reject `thinking.type="disabled"` with HTTP 400. Every model
// approval then failed with the provider's error and fell back to the failure
// action, so the approval feature looked permanently broken.
//
// The caller passes the profile's own thinking setting; an empty value means
// "let the provider decide" (the default), and an explicit "disabled" still
// wins for models that allow it.
func streamCollectText(ctx context.Context, prov llm.Provider, system, user string) (string, error) {
	return streamCollectTextWithThinking(ctx, prov, system, user, "")
}

// streamCollectTextWithThinking is streamCollectText with an explicit
// thinking mode ("" = provider default).
func streamCollectTextWithThinking(ctx context.Context, prov llm.Provider, system, user, thinking string) (string, error) {
	temp := 0.0
	req := llm.CompletionRequest{
		System:      []string{system},
		Messages:    []llm.Message{llm.UserText(user)},
		MaxTokens:   1024,
		Temperature: &temp,
		Thinking:    thinking,
	}
	var sb strings.Builder
	for ev, err := range prov.Stream(ctx, req) {
		if err != nil {
			return "", err
		}
		if ev.Type == llm.SETextDelta {
			sb.WriteString(ev.Text)
		}
	}
	return sb.String(), nil
}

// --- intercept rule CRUD ---

func (s *Server) interceptListRules(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	rules, err := pg.ListInterceptRules()
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	if rules == nil {
		rules = []db.InterceptRule{}
	}
	writeJSON(w, 200, map[string]any{"rules": rules})
}

func (s *Server) interceptCreateRule(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req interceptRuleReq
	if err := decode(r, &req); err != nil {
		writeDecodeErr(w, err)
		return
	}
	if err := validateInterceptRuleReq(req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rule, err := pg.CreateInterceptRule(req.Name, req.MatchTarget, req.MatchType, req.Pattern, req.Action, req.Message, req.Priority, req.Enabled, req.TimeoutEnabled, req.TimeoutSeconds, req.TimeoutAction)
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	s.m.interceptor.Invalidate()
	writeJSON(w, 200, rule)
}

func (s *Server) interceptUpdateRule(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "规则 id 无效")
		return
	}
	var req interceptRuleReq
	if err := decode(r, &req); err != nil {
		writeDecodeErr(w, err)
		return
	}
	if err := validateInterceptRuleReq(req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rule, err := pg.UpdateInterceptRule(id, req.Name, req.MatchTarget, req.MatchType, req.Pattern, req.Action, req.Message, req.Priority, req.Enabled, req.TimeoutEnabled, req.TimeoutSeconds, req.TimeoutAction)
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	s.m.interceptor.Invalidate()
	writeJSON(w, 200, rule)
}

func (s *Server) interceptDeleteRule(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "规则 id 无效")
		return
	}
	if err := pg.DeleteInterceptRule(id); err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	s.m.interceptor.Invalidate()
	writeJSON(w, 200, map[string]any{"deleted": id})
}

func (s *Server) interceptToggleRule(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "规则 id 无效")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &req); err != nil {
		writeDecodeErr(w, err)
		return
	}
	if err := pg.ToggleInterceptRule(id, req.Enabled); err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	s.m.interceptor.Invalidate()
	writeJSON(w, 200, map[string]any{"ok": true, "enabled": req.Enabled})
}

// --- pending (ask) ---

func (s *Server) interceptListPending(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	pending, err := pg.ListPendingIntercepts()
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	if pending == nil {
		pending = []db.InterceptPending{}
	}
	writeJSON(w, 200, map[string]any{"pending": pending})
}

func (s *Server) interceptGetOne(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "待审项 id 无效")
		return
	}
	p, err := pg.GetInterceptPending(id)
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	if p == nil {
		writeErr(w, 404, "待审项不存在")
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) interceptListTaskItems(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	taskID := r.PathValue("taskID")
	if taskID == "" {
		writeErr(w, 400, "任务 id 无效")
		return
	}
	q := r.URL.Query()
	filter, err := interceptFilterParams(q)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if q.Get("page") == "" && q.Get("size") == "" && filter == (db.InterceptApprovalFilter{}) {
		items, err := pg.ListTaskIntercepts(taskID)
		if err != nil {
			writeErrRedacted(w, 500, err.Error())
			return
		}
		if items == nil {
			items = []db.InterceptApprovalRow{}
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
		return
	}
	page, size := interceptPageParams(q)
	items, total, err := pg.ListTaskInterceptsPage(taskID, page, size, filter)
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	if items == nil {
		items = []db.InterceptApprovalRow{}
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}

func (s *Server) interceptHistory(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	q := r.URL.Query()
	filter, err := interceptFilterParams(q)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if q.Get("page") == "" && q.Get("size") == "" && filter == (db.InterceptApprovalFilter{}) {
		items, err := pg.ListAllIntercepts(200)
		if err != nil {
			writeErrRedacted(w, 500, err.Error())
			return
		}
		if items == nil {
			items = []db.InterceptApprovalRow{}
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
		return
	}
	page, size := interceptPageParams(q)
	items, total, err := pg.ListAllInterceptsPage(page, size, filter)
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	if items == nil {
		items = []db.InterceptApprovalRow{}
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}

// interceptHistoryStats 按规则聚合拦截命中数（GET /api/intercept/history/stats）。
// 与列表接口共用 interceptFilterParams，保证"筛出来的列表"与"聚合统计"口径一致。
// 单独一个端点而不是塞进列表响应：聚合要扫全量，列表只取一页，混在一起会让
// 每次翻页都多一次全表聚合。
func (s *Server) interceptHistoryStats(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	filter, err := interceptFilterParams(r.URL.Query())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	stats, err := pg.InterceptCountsByRule(filter)
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	if stats == nil {
		stats = []db.InterceptRuleStat{}
	}
	writeJSON(w, 200, map[string]any{"rules": stats})
}

func interceptFilterParams(q url.Values) (db.InterceptApprovalFilter, error) {
	filter := db.InterceptApprovalFilter{
		Status:         q.Get("status"),
		DecisionSource: q.Get("decision_source"),
		Search:         strings.TrimSpace(q.Get("q")),
		CreatedFrom:    q.Get("from"),
		CreatedTo:      q.Get("to"),
	}
	if utf8.RuneCountInString(filter.Search) > 200 {
		return filter, fmt.Errorf("q 不能超过 200 个字符")
	}
	if strings.HasPrefix(filter.Search, "#") {
		id, err := strconv.ParseInt(strings.TrimPrefix(filter.Search, "#"), 10, 64)
		if err != nil || id <= 0 {
			return filter, fmt.Errorf("审批编号必须为 # 后接正整数")
		}
	}
	for name, value := range map[string]string{"from": filter.CreatedFrom, "to": filter.CreatedTo} {
		if value == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return filter, fmt.Errorf("%s 必须是 YYYY-MM-DD 日期", name)
		}
	}
	if filter.CreatedFrom != "" && filter.CreatedTo != "" && filter.CreatedFrom > filter.CreatedTo {
		return filter, fmt.Errorf("from 不能晚于 to")
	}
	switch filter.Status {
	case "", "pending", "allowed", "denied", "timeout":
	default:
		return filter, fmt.Errorf("status 必须是 pending、allowed、denied 或 timeout")
	}
	switch filter.DecisionSource {
	case "", "model", "rule", "manual", "unknown":
	default:
		return filter, fmt.Errorf("decision_source 必须是 model、rule、manual 或 unknown")
	}
	return filter, nil
}

func interceptPageParams(q url.Values) (int, int) {
	page := atoiDefault(q.Get("page"), 1)
	size := atoiDefault(q.Get("size"), 20)
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func (s *Server) interceptDecide(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "待审项 id 无效")
		return
	}
	var req struct {
		Decision string `json:"decision"` // "allowed" | "denied"
	}
	if err := decode(r, &req); err != nil {
		writeDecodeErr(w, err)
		return
	}
	if req.Decision != "allowed" && req.Decision != "denied" {
		writeErr(w, 400, "decision 必须是 allowed 或 denied")
		return
	}
	if err := s.m.interceptor.Decide(id, req.Decision == "allowed"); err != nil {
		if errors.Is(err, intercept.ErrAlreadyDecided) {
			writeErr(w, 409, err.Error())
			return
		}
		writeErrRedacted(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// --- tool-config (全局工具拦截范围) ---

// interceptGetToolConfig returns the list of tool names that are currently
// configured to enter the intercept rule system.
func (s *Server) interceptGetToolConfig(w http.ResponseWriter, r *http.Request) {
	tools, err := s.m.interceptor.GetEnabledTools()
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"enabled_tools": tools})
}

// interceptSetToolConfig replaces the list of tool names that should enter
// the intercept rule system.
func (s *Server) interceptSetToolConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EnabledTools []string `json:"enabled_tools"`
	}
	if err := decode(r, &req); err != nil {
		writeDecodeErr(w, err)
		return
	}
	if req.EnabledTools == nil {
		req.EnabledTools = []string{}
	}
	if err := s.m.interceptor.SetEnabledTools(req.EnabledTools); err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// --- LLM fallback judge config (全局模型兜底) ---

// interceptGetJudgeConfig returns the resolved judge configuration. Prompt is the
// effective prompt (built-in template when unset), so the UI can prefill it.
func (s *Server) interceptGetJudgeConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.m.interceptor.GetJudgeConfig())
}

// interceptSetJudgeConfig persists the judge configuration.
func (s *Server) interceptSetJudgeConfig(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := decode(r, &raw); err != nil {
		writeDecodeErr(w, err)
		return
	}
	var req intercept.JudgeConfig
	if err := json.Unmarshal(raw, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if _, present := fields["confidence_threshold"]; !present {
		req.ConfidenceThreshold = 0.55
	} else if req.ConfidenceThreshold < 0 || req.ConfidenceThreshold > 1 {
		writeErr(w, 400, "confidence_threshold 必须在 0 到 1 之间")
		return
	}
	switch req.FailAction {
	case "allow", "ask", "deny":
	default:
		writeErr(w, 400, "fail_action 必须是 allow、ask 或 deny")
		return
	}
	switch req.AskTimeoutAction {
	case "allow", "deny":
	default:
		writeErr(w, 400, "ask_timeout_action 必须是 allow 或 deny")
		return
	}
	switch req.IncompleteScriptAction {
	case "":
		req.IncompleteScriptAction = intercept.DefaultIncompleteScriptAction
	case "ask", "model":
	default:
		writeErr(w, 400, "incomplete_script_action 必须是 ask 或 model")
		return
	}
	if err := req.ValidateRetry(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.m.interceptor.SetJudgeConfig(req); err != nil {
		if strings.Contains(err.Error(), "confidence_threshold") {
			writeErr(w, 400, err.Error())
		} else {
			writeErrRedacted(w, 500, err.Error())
		}
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// interceptJudgeUsage returns the fallback judge's cumulative token spend plus a
// recent daily series, for the config page. ?days bounds the daily series (default 30).
func (s *Server) interceptJudgeUsage(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeJSON(w, 200, db.JudgeUsage{Daily: []db.JudgeDayUsage{}})
		return
	}
	days := atoiDefault(r.URL.Query().Get("days"), 30)
	usage, err := pg.JudgeUsageStats(days)
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, usage)
}

// --- helpers ---

type interceptRuleReq struct {
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	Priority       int    `json:"priority"`
	MatchTarget    string `json:"match_target"`
	MatchType      string `json:"match_type"`
	Pattern        string `json:"pattern"`
	Action         string `json:"action"`
	Message        string `json:"message"`
	TimeoutEnabled bool   `json:"timeout_enabled"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	TimeoutAction  string `json:"timeout_action"`
}

func validateInterceptRuleReq(req interceptRuleReq) error {
	if req.Name == "" {
		return fmt.Errorf("name 不能为空")
	}
	switch req.MatchTarget {
	case "tool_name", "tool_input":
	default:
		return fmt.Errorf("match_target 必须是 tool_name 或 tool_input")
	}
	switch req.MatchType {
	case "string", "regex":
	default:
		return fmt.Errorf("match_type 必须是 string 或 regex")
	}
	if req.Pattern == "" {
		return fmt.Errorf("pattern 不能为空")
	}
	switch req.Action {
	case "allow", "deny", "ask":
	default:
		return fmt.Errorf("action 必须是 allow、deny 或 ask")
	}
	if req.MatchType == "regex" {
		if _, err := regexp.Compile(req.Pattern); err != nil {
			return fmt.Errorf("pattern 不是有效正则：%w", err)
		}
	}
	return nil
}

func (s *Server) interceptDetail(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "审批 id 无效")
		return
	}
	detail, err := pg.GetInterceptDetail(id)
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	if detail == nil {
		writeErr(w, 404, "审批不存在")
		return
	}
	writeJSON(w, 200, detail)
}

// The navigation endpoint returns only the original call and its paired result.
func (s *Server) interceptExecution(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "审批 id 无效")
		return
	}
	target, err := pg.GetInterceptExecution(id)
	if errors.Is(err, db.ErrInterceptTaskDeleted) || errors.Is(err, db.ErrInterceptSessionDeleted) {
		writeErr(w, http.StatusGone, err.Error())
		return
	}
	if errors.Is(err, db.ErrInterceptExecutionUnavailable) {
		writeErr(w, 409, err.Error())
		return
	}
	if err != nil {
		writeErrRedacted(w, 500, err.Error())
		return
	}
	if target == nil {
		// Conversation deletion cascades approval rows. A stale source link still
		// carries its conversation ID, allowing a precise message without retaining
		// deleted conversations or changing their deletion semantics.
		if convID, parseErr := strconv.ParseInt(r.URL.Query().Get("conversation"), 10, 64); parseErr == nil && convID > 0 {
			conv, getErr := pg.GetConversation(convID)
			if getErr != nil {
				writeErrRedacted(w, 500, getErr.Error())
				return
			}
			if conv == nil {
				writeErr(w, http.StatusGone, "对话已被删除")
				return
			}
		}
		writeErr(w, 404, "审批记录已被删除或不存在")
		return
	}
	writeJSON(w, 200, map[string]any{"conversation_id": target.ConversationID, "task_id": target.TaskID, "session": target.Session, "seq": target.Seq, "items": activityDTOs(target.Items)})
}
