// scope_guard.go：把任务授权范围（ScopeMatcher）接到 Bash / 交互式 shell 上。
//
// 背景：run_tool 是"受管执行入口"，平台在那里能看到结构化参数并校验目标。但
// Bash 一直是自由文本通道 —— run_tool.go 的包注释写得很清楚："Bash 仍然可以跑
// 通用命令（ls/cat/curl 等）"。结果是范围约束只覆盖了"规规矩矩走受管入口"的
// 工具，而 worker 只要用 curl / nmap / nuclei 直接打，就能绕过范围校验。
//
// 本文件补的就是这条缝：从命令文本里提取目标，逐个过 ScopeMatcher。
//
// 边界（有意为之）：
//   - **只做高置信度提取**（见 scopetext 包），解析不出目标就放行并记审计；
//   - 文本层有天然盲区：脚本执行、管道、变量拼接、base64 在字符串层面无法还原，
//     这部分由网络层（代理）兜底，两层都不能少；
//   - 只对 Bash / shell_open / shell_send 生效（guard 的 preToolUse 只对这三个
//     取命令文本）。其他工具若有自己的受管入口，按各自的路径校验。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"artex/agent"
	"artex/db"
	"artex/scopetext"
)

// scopeCheckerForTask 构造某个任务的命令级范围判定器，注入给该任务的 Guard。
// 返回 nil 表示不做判定（没有范围配置时不拦 —— 空 scope 的任务靠人工把控，
// 这里不额外制造"什么都不能打"的死锁；run_tool 的 fail-closed 语义不变）。
func (s *Server) scopeCheckerForTask(taskID int64) func(ctx context.Context, tool, command string) (bool, string) {
	if taskID <= 0 {
		return nil
	}
	return func(ctx context.Context, tool, command string) (bool, string) {
		return s.checkCommandScope(ctx, taskID, tool, command)
	}
}

// checkCommandScope 是实际判定：提取目标 → 逐个过 matcher → 有越界就拦。
func (s *Server) checkCommandScope(ctx context.Context, taskID int64, tool, command string) (bool, string) {
	targets := scopetext.Targets(command)
	if len(targets) == 0 {
		// 解析不出目标：按既定口径放行，但要留审计 —— 否则事后无法回答
		// "有多少命令逃过了范围判定"，也就无法评估这条防线的实际覆盖率。
		s.auditCommandScope(taskID, tool, command, nil, "")
		return false, ""
	}
	matcher, err := s.taskScopeMatcher(taskID)
	if err != nil {
		// 取不到范围就**不拦**：这里和 run_tool 的 fail-closed 不同，因为
		// Bash 是通用通道，读库抖动就把所有命令卡死会造成大面积误伤。
		// 取不到范围意味着平台无法判断，这种情况记审计并放行，由网络层兜底。
		s.auditCommandScope(taskID, tool, command, targets, "范围读取失败: "+err.Error())
		return false, ""
	}
	if !matcher.HasAny {
		// 任务没有配置任何授权范围：不在这一层制造全局拒绝（run_tool 的
		// fail-closed 语义保持不变，那里是受管入口、风险与影响面都不同）。
		s.auditCommandScope(taskID, tool, command, targets, "任务未配置授权范围，跳过命令级范围校验")
		return false, ""
	}
	var out []string
	for _, t := range targets {
		if !matcher.InScope(t) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return false, ""
	}
	// 指引必须指向**真正可行**的动作：add_task_scope 现在只允许"细化"（为已授权
	// 根域加子域等），不能新增范围；扩范围只能由人来做。写成"去调 add_task_scope"
	// 会让 agent 反复撞墙（实测就是这样）。
	reason := fmt.Sprintf(
		"【平台管控·范围校验】命令里的目标 %s 不在本任务授权范围内，已拒绝执行。"+
			"扩大授权需人工审批：请用 add_task_hint 说明理由并请求补充授权，不要重试同一目标，"+
			"也不要试图用其它工具绕过。",
		quoteList(out))
	s.auditCommandScope(taskID, tool, command, out, reason)
	return true, reason
}

// auditCommandScope 把命令级范围判定的结果写进活动流，供复盘时追查。
// 失败不阻塞主流程：审计写不进去不该改变"拦不拦"的结论。
func (s *Server) auditCommandScope(taskID int64, tool, command string, targets []string, note string) {
	t, ok := s.m.Task(fmt.Sprint(taskID))
	if !ok || t == nil || t.Store == nil {
		return
	}
	kind := "scope_check"
	isErr := false
	var summary string
	switch {
	case note != "" && strings.HasPrefix(note, "【平台管控"):
		kind = "scope_denied"
		isErr = true
		summary = fmt.Sprintf("%s：%s", tool, note)
	case len(targets) == 0:
		summary = fmt.Sprintf("%s：命令里未识别出网络目标，已放行并记录", tool)
	default:
		summary = fmt.Sprintf("%s：%d 个目标均在范围内或跳过校验（%s）", tool, len(targets), note)
	}
	detail := map[string]any{"tool": tool, "targets": targets, "command": clipRunes(command, 2000), "note": note}
	b, _ := json.Marshal(detail)
	_, _ = t.Store.AppendActivity(db.Activity{
		Worker: "guard", Kind: kind, Tool: tool,
		IsError: isErr, Summary: summary, Detail: string(b),
	})
}

func quoteList(xs []string) string {
	parts := make([]string, 0, len(xs))
	for _, x := range xs {
		parts = append(parts, fmt.Sprintf("%q", x))
	}
	return strings.Join(parts, "、")
}

// taskIDFromCtx 取当前运行的 taskID（0 表示非任务上下文，如对话）。
func taskIDFromCtx(ctx context.Context) int64 {
	return agent.RunInfoFrom(ctx).TaskID
}

// checkURLScope 按单个 URL/主机做范围校验，供自建请求的工具（自定义 http 工具、
// 未来的其它直连工具）复用。与 checkCommandScope 的区别只是"目标已确定"，
// 不需要文本提取，因此不做"解析不出就放行"的兜底 —— 目标为空或越界都拒绝。
func (s *Server) checkURLScope(ctx context.Context, tool, rawURL string) (bool, string) {
	taskID := taskIDFromCtx(ctx)
	if taskID <= 0 {
		return false, "" // 非任务上下文（对话/主 agent 无任务绑定）不在此层约束
	}
	matcher, err := s.taskScopeMatcher(taskID)
	if err != nil || !matcher.HasAny {
		// 与命令级一致：取不到范围或任务未配置范围时不在这一层拒绝。
		s.auditCommandScope(taskID, tool, "url="+rawURL, []string{rawURL}, "范围不可用或未配置，跳过")
		return false, ""
	}
	if matcher.InScope(rawURL) {
		return false, ""
	}
	msg := fmt.Sprintf("【平台管控·范围校验】目标 %q 不在本任务授权范围内，已拒绝执行。", rawURL)
	s.auditCommandScope(taskID, tool, "url="+rawURL, []string{rawURL}, msg)
	return true, msg
}

// proxyScopeGate 构造代理层的任务范围闸门。目标主机来自 CONNECT 的 r.Host 或
// 明文请求的 URL host —— 两者都只到主机名粒度，没有 scheme/path，因此直接
// 交给 ScopeMatcher 的 host 分支判定。
//
// 与命令级（checkCommandScope）的口径差异：这里没有"解析不出就放行"的问题，
// 因为主机名是确定的。唯一的放行条件是"任务没有配置任何范围"——那种情况下
// 平台确实没有依据拒绝，硬拒会打断正常任务。
func (s *Server) proxyScopeGate(taskID int64, host string) (string, bool) {
	matcher, err := s.taskScopeMatcher(taskID)
	if err != nil || !matcher.HasAny {
		return "", false
	}
	if matcher.InScope(host) {
		return "", false
	}
	reason := fmt.Sprintf(
		"【平台管控·范围校验】代理目标 %q 不在本任务授权范围内，已拒绝。",
		host)
	s.auditCommandScope(taskID, "proxy", "CONNECT/GET "+host, []string{host}, reason)
	return reason, true
}
