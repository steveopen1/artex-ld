// blockmessage.go：拦截消息模板——把「平台管控」文案从硬编码改成可配置模板。
//
// 为什么需要它：原实现把提示词写死在 systemBlockMessage 里，规则表里用户自定义的
// message 只能被塞进「原因：{...}」这一个位置，塞不进工具名、命令、规则名，也改不了
// 前缀与结尾的纪律声明。红队场景下这句话是模型唯一能看到的「为什么被拒、接下来该
// 干什么」，措辞必须可调：
//   - 有的队伍要求明确写出「这是平台策略、不是目标防御」，避免模型去绕 WAF；
//   - 有的要求带上被拦命令，方便模型自己判断是参数问题还是方向问题；
//   - 有的要求语气更强硬/更简短以省 token。
//
// 模板变量用单花括号（{tool} / {rule} / {reason} / {command}），与 Go 的 %s 和
// 提示词里的双花括号模板互不冲突。未知变量原样保留，避免拼错变量导致内容凭空消失。
package guard

import (
	"strings"
)

// BlockMessageVars 是一次拦截消息渲染时可用的变量集合。
type BlockMessageVars struct {
	Tool    string // 工具名，如 Bash
	Rule    string // 命中的规则名；LLM 裁判命中时为空
	Reason  string // 规则/裁判给出的人类可读原因
	Command string // 被拦的 shell 命令或 shell_send 文本；非 shell 工具为空
	Action  string // deny | ask_denied | cancelled
}

// DefaultBlockMessageTemplate 是既有行为的等价默认值：保留「平台管控·非目标防御」
// 这个关键前缀与「此操作被禁止」的结尾。它不是装饰——原有注释解释了模型会把裸
// reason（"禁止执行此工具"）误读成目标侧 403 并尝试绕过，那正是这个前缀存在的理由。
const DefaultBlockMessageTemplate = "【平台管控·非目标防御】此调用被平台拦截。原因：{reason}。此操作被禁止。"

// maxCommandInMessage 限制命令回显长度：拦截消息会进下一轮上下文，超长命令（比如
// 一整个 base64 payload）会把 prompt 撑大，且对判断毫无帮助。
const maxCommandInMessage = 200

// RenderBlockMessage 用模板渲染拦截消息。template 为空时用默认模板。
// 渲染结果为空（模板只有空白）时回落到默认模板，避免模型收到一条空提醒。
func RenderBlockMessage(template string, v BlockMessageVars) string {
	tpl := strings.TrimSpace(template)
	if tpl == "" {
		tpl = DefaultBlockMessageTemplate
	}
	if strings.TrimSpace(v.Command) != "" {
		v.Command = strings.TrimSpace(v.Command)
		if r := []rune(v.Command); len(r) > maxCommandInMessage {
			v.Command = string(r[:maxCommandInMessage]) + "…"
		}
	}
	out := strings.TrimSpace(expandBlockVars(tpl, v))
	if out == "" {
		return expandBlockVars(DefaultBlockMessageTemplate, v)
	}
	return out
}

// expandBlockVars 做单花括号替换。只认白名单里的变量名，其余原样保留，
// 这样模板里写的 JSON、C 代码片段、正则都不会被误替换。
func expandBlockVars(tpl string, v BlockMessageVars) string {
	pairs := []struct{ key, val string }{
		{"{tool}", v.Tool},
		{"{rule}", v.Rule},
		{"{reason}", v.Reason},
		{"{command}", v.Command},
		{"{action}", v.Action},
	}
	var b strings.Builder
	b.Grow(len(tpl) + 32)
	rest := tpl
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:i])
		rest = rest[i:]
		matched := false
		for _, p := range pairs {
			if strings.HasPrefix(rest, p.key) {
				b.WriteString(p.val)
				rest = rest[len(p.key):]
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte('{')
			rest = rest[1:]
		}
	}
}

// escapeForLog 是命令进日志时的最小处理：保留原文，只把控制字符换成空格，
// 避免一条含 \r 的命令把日志行拆散。
func escapeForLog(s string) string {
	if s == "" {
		return ""
	}
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
	cleaned = strings.TrimSpace(cleaned)
	if r := []rune(cleaned); len(r) > maxCommandInMessage {
		cleaned = string(r[:maxCommandInMessage]) + "…"
	}
	return cleaned
}
