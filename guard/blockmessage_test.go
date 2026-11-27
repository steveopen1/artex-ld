package guard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"artex/db"
	"artex/intercept"
	"norma/hook"
)

// TestRenderBlockMessageDefault 默认模板的文案必须稳定：模型看到的就是这段。
// 除产品名外措辞与历史一致（去标识化只去掉平台名，不改语义）。
func TestRenderBlockMessageDefault(t *testing.T) {
	got := RenderBlockMessage("", BlockMessageVars{Reason: "禁止删除数据"})
	// 去标识化后的默认文案：不再带产品名（模型会读到这段，带产品名等于自报家门）。
	want := "【平台管控·非目标防御】此调用被平台拦截。原因：禁止删除数据。此操作被禁止。"
	if got != want {
		t.Fatalf("default render = %q\nwant           %q", got, want)
	}
}

// TestRenderBlockMessageCustom 自定义模板必须能吃到工具名/规则名/命令。
// 这是「塞不进工具名/命令」那条缺口的回归点。
func TestRenderBlockMessageCustom(t *testing.T) {
	got := RenderBlockMessage(
		"[策略拦截] 规则={rule} 工具={tool} 命令={command}。原因：{reason}。换个方向，不要重试。",
		BlockMessageVars{
			Tool: "Bash", Rule: "no-write", Reason: "只读授权",
			Command: `curl -X DELETE http://t/api/1`,
		},
	)
	for _, want := range []string{"规则=no-write", "工具=Bash", "-X DELETE", "原因：只读授权"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered %q missing %q", got, want)
		}
	}
}

// TestRenderBlockMessageUnknownVarKept 未知变量原样保留，避免模板里的 JSON /
// 代码片段被吃掉。
func TestRenderBlockMessageUnknownVarKept(t *testing.T) {
	got := RenderBlockMessage("json={notavar} tool={tool}", BlockMessageVars{Tool: "Bash"})
	if !strings.Contains(got, "{notavar}") {
		t.Fatalf("unknown var was eaten: %q", got)
	}
	if !strings.Contains(got, "tool=Bash") {
		t.Fatalf("known var not expanded: %q", got)
	}
}

// TestRenderBlockMessageTruncatesCommand 超长命令要截断：提醒会进下一轮上下文。
func TestRenderBlockMessageTruncatesCommand(t *testing.T) {
	long := strings.Repeat("A", 5000)
	got := RenderBlockMessage("cmd={command}", BlockMessageVars{Command: long})
	if len([]rune(got)) > maxCommandInMessage+64 {
		t.Fatalf("command not truncated: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncation marker missing: %q", got[len(got)-8:])
	}
}

// TestRenderBlockMessageEmptyTemplateFallsBack 全空白模板必须回落默认，
// 否则模型会收到一条空提醒。
func TestRenderBlockMessageEmptyTemplateFallsBack(t *testing.T) {
	got := RenderBlockMessage("   ", BlockMessageVars{Reason: "x"})
	if !strings.Contains(got, "平台管控") {
		t.Fatalf("blank template did not fall back: %q", got)
	}
}

// TestAppendTrafficEvidenceToMessage 流量证据必须作为独立段落附加：
// 自定义模板省略 {command} 时证据仍然出现。
func TestAppendTrafficEvidenceToMessage(t *testing.T) {
	g := newGuard(nil)
	g.SetBlockMessageTemplate(func() string { return "禁止 {tool}" })
	g.SetTrafficMatcher(func(context.Context, string, string) []BlockedTraffic {
		return []BlockedTraffic{
			{ID: "tr-1", Method: "DELETE", URL: "http://t/api/orders/1", Status: 405},
			{ID: "tr-2", Method: "DELETE", URL: "http://t/api/orders/2", Status: 403},
		}
	})
	msg := g.blockMessage(context.Background(),
		hookEvent("Bash", `{"command":"curl -X DELETE http://t/api/orders/1"}`),
		intercept.Decision{Action: "deny", Message: "只读授权"},
		"deny", "curl -X DELETE http://t/api/orders/1")

	if !strings.Contains(msg, "禁止 Bash") {
		t.Fatalf("custom template not applied: %q", msg)
	}
	if !strings.Contains(msg, "对应流量证据") {
		t.Fatalf("traffic evidence paragraph missing: %q", msg)
	}
	if !strings.Contains(msg, "traffic_id=tr-1") || !strings.Contains(msg, "DELETE http://t/api/orders/1") {
		t.Fatalf("evidence rows missing: %q", msg)
	}
}

// TestEmitBlockedWritesActivity deny 必须往会话流写一条提醒——这是「拦截后向
// 会话注入」（而不仅是 tool_result 内联）的核心保证。
func TestEmitBlockedWritesActivity(t *testing.T) {
	g := newGuard(nil)
	var got []db.Activity
	ctx := intercept.WithTaskContext(context.Background(), "7", "work#1", func(a db.Activity) {
		got = append(got, a)
	})
	g.emitBlocked(ctx, hookEvent("Bash", `{"command":"rm -rf /"}`),
		intercept.Decision{Action: "deny", RuleName: "no-destructive", Message: "禁止破坏性操作"},
		"deny", "rm -rf /", "【平台管控】禁止")

	if len(got) != 1 {
		t.Fatalf("emitted %d activities, want 1", len(got))
	}
	if got[0].Kind != "intercept_denied" {
		t.Fatalf("activity kind = %q, want intercept_denied", got[0].Kind)
	}
	if !strings.Contains(got[0].Summary, "Bash") {
		t.Fatalf("summary missing tool: %q", got[0].Summary)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(got[0].Detail), &detail); err != nil {
		t.Fatalf("detail not json: %v", err)
	}
	for _, k := range []string{"tool", "action", "rule", "reason", "command", "message"} {
		if _, ok := detail[k]; !ok {
			t.Fatalf("detail missing %q: %s", k, got[0].Detail)
		}
	}
}

// TestEmitBlockedNoopWithoutContext 无任务上下文时不应 panic 也不应产生活动。
func TestEmitBlockedNoopWithoutContext(t *testing.T) {
	g := newGuard(nil)
	g.emitBlocked(context.Background(), hookEvent("Bash", `{}`),
		intercept.Decision{Action: "deny"}, "deny", "x", "m")
}

func hookEvent(tool, input string) hook.Event {
	return hook.Event{ToolName: tool, Input: []byte(input)}
}
