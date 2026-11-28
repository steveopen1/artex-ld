// replay_tool.go：http_replay —— 从已录制的流量里挑一条请求，改参数/换身份后重放。
//
// 为什么需要它：平台已经能"录"（MITM 代理逐请求留存）和"看"（traffic_search /
// traffic_get / traffic_blob 三条只读工具），但 worker 没有"改着重打"的能力。
// 于是 worker 想验证一个越权/注入假设时，只能手搓 curl 重写一遍请求 ——
// 这有两个后果：
//  1. 丢掉了原始请求的全部细节（Cookie 轮换、自定义头、Content-Type、签名参数），
//     重写出来的请求和真实流量不是同一条，"复现"其实没复现；
//  2. 手搓的 curl 走 Bash，绕过 run_tool 的契约与范围校验（见 run_tool.go 包注释）。
//     本工具走 executeRepeaterSend，与 UI 的「请求重发」共用同一套 SSRF 防护、
//     重定向策略与 hop-by-hop 过滤，并且显式做任务范围校验。
//
// 与 UI 重发的差别：UI 是人手工填，本工具以"已录流量"为默认值，只覆盖用户
// 显式指定的字段 —— 这样一次重放的最小改动面就是那个被怀疑的参数本身。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"artex/agent"
	"artex/traffic"
	"norma/permission"
	actool "norma/tool"
)

// replayMaxBody 是重放请求体的上限，与 repeater 的 2 MiB 请求上限保持一致。
const replayMaxBody = repeaterMaxBody

// httpReplayTool 构造 http_replay 工具。绑定在 tools 表里（默认 worker），
// 因此只能被绑定的 agent 看到（ToolResolve 按绑定过滤）。
func (s *Server) httpReplayTool() actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: "http_replay",
		Description: "从已录制的流量中取一条请求，覆盖指定字段后重新发送。" +
			"典型用法：验证越权（改 Cookie/Authorization 换身份）、验证注入（改参数值）、" +
			"验证方法/路径绕过（改 method/url）。默认沿用原始请求的 method/url/headers/body，" +
			"只覆盖你显式给出的字段。返回状态码、响应头与响应体预览；" +
			"若配 use_recording_proxy=true（默认），这次重放本身也会被录成新的流量，" +
			"可用 traffic_search 找到并作为漏洞证据引用。",
		Schema: objSchema(map[string]any{
			"id": strParam("必填。已录制流量的 exchange id（先用 traffic_search 找到，形如 128）"),
			"method": strParam("可选。覆盖 HTTP 方法，如 GET/POST/PUT/DELETE/PATCH/HEAD/OPTIONS"),
			"url":    strParam("可选。覆盖完整 URL（含 scheme）。必须在本任务授权范围内"),
			"headers": map[string]any{
				"type":        "object",
				"description": "可选。覆盖/新增的请求头。键为头名。覆盖 Cookie/Authorization 即可换身份",
				"additionalProperties": map[string]any{"type": "string"},
			},
			"remove_headers": map[string]any{
				"type":        "array",
				"description": "可选。要删除的请求头名（例如删掉原请求里的 Authorization 看是否仍被放行）",
				"items":       map[string]any{"type": "string"},
			},
			"body": strParam("可选。覆盖请求体"),
			"http_version": strParam("可选。HTTP/1.1 或 HTTP/2.0；省略=沿用原始请求的协议"),
			"timeout_ms": map[string]any{
				"type":        "integer",
				"description": "可选。请求超时毫秒数，默认 30000，范围 1000~120000",
			},
			"use_recording_proxy": map[string]any{
				"type":        "boolean",
				"description": "可选。是否经录制代理发送（默认 true）。置 false 则这次重放不被记录",
			},
			"insecure_skip_verify": map[string]any{
				"type":        "boolean",
				"description": "可选。跳过 TLS 校验（默认 false）",
			},
		}, "id"),
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return s.runHTTPReplay(ctx, in)
		},
	})
}

type httpReplayArgs struct {
	ID                 string            `json:"id"`
	Method             string            `json:"method"`
	URL                string            `json:"url"`
	Headers            map[string]string `json:"headers"`
	RemoveHeaders      []string          `json:"remove_headers"`
	Body               *string           `json:"body"`
	HTTPVersion        string            `json:"http_version"`
	TimeoutMs          int               `json:"timeout_ms"`
	UseRecordingProxy  *bool             `json:"use_recording_proxy"`
	InsecureSkipVerify bool              `json:"insecure_skip_verify"`
}

func (s *Server) runHTTPReplay(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
	var a httpReplayArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return actool.Errorf(fmt.Sprintf("参数无效: %v", err)), nil
	}
	id := strings.TrimSpace(a.ID)
	if id == "" {
		return actool.Errorf("id 必填：先用 traffic_search 找到目标流量的 exchange id"), nil
	}
	// 只要求"流量存储可用"，**不要求当前正在录制**。
	// 重放的依据是历史已录的那条请求，与此刻是否开着录制无关；之前把
	// TrafficEnabled 当门槛，导致"有历史流量但录制关着"时重放被无谓拒绝
	// （实测踩到：库里有 32 条 127.0.0.1 的录制，却报"录制未开启"）。
	if s.m.Traffic() == nil {
		return actool.Errorf("流量存储不可用，无法重放"), nil
	}
	recording := s.m.TrafficEnabled()

	// 取原始请求。ReadEvidence 的回调期间才保证 blob reader 有效，所以解析与
	// 覆盖都必须在这个回调里做完 —— 出了回调原始请求体就读不到了。
	var (
		method, rawURL, reqHead, httpVersion string
		origBody                             []byte
		origHeaders                          http.Header
		found                                bool
	)
	err := s.m.Traffic().ReadEvidence(ctx, []string{id}, func(e traffic.EvidenceExchange) error {
		found = true
		method = strings.ToUpper(strings.TrimSpace(e.Method))
		rawURL = strings.TrimSpace(e.URL)
		reqHead = e.ReqHead
		// 原始请求体可能很大，但重放本来就有 2 MiB 上限；超出部分截断而不是
		// 整体失败 —— 头/URL 仍然可用于重放，只是 body 不完整。
		if e.Request != nil {
			b, rerr := io.ReadAll(io.LimitReader(e.Request, replayMaxBody+1))
			if rerr != nil {
				return fmt.Errorf("读取原始请求体失败: %w", rerr)
			}
			if len(b) > replayMaxBody {
				b = b[:replayMaxBody]
			}
			origBody = b
		}
		// req_head 的第一行是请求行，里面带协议版本，供"未显式指定 http_version"时沿用。
		headers, ver := parseRawRequestHead(reqHead)
		origHeaders = headers
		httpVersion = ver
		// URL 缺失时从请求行补（部分老数据 exchanges.url 为空）。
		if rawURL == "" {
			if u := requestLineURL(reqHead); u != "" {
				rawURL = u
			}
		}
		return nil
	})
	if err != nil {
		return actool.Errorf(fmt.Sprintf("读取流量 %s 失败: %v", id, err)), nil
	}
	if !found {
		return actool.Errorf(fmt.Sprintf("流量 %s 不存在", id)), nil
	}
	if rawURL == "" {
		return actool.Errorf(fmt.Sprintf("流量 %s 没有可用 URL，无法重放", id)), nil
	}
	// 相对 URL（代理录到的是 path-only）补成绝对 URL：原始 host 从 Host 头取。
	if !strings.Contains(rawURL, "://") {
		host := origHeaders.Get("Host")
		if host == "" {
			return actool.Errorf(fmt.Sprintf("流量 %s 的 URL 是相对路径且缺少 Host 头，无法确定目标", id)), nil
		}
		scheme := "http"
		if strings.Contains(reqHead, "https://") {
			scheme = "https"
		}
		rawURL = scheme + "://" + host + rawURL
	}

	// 覆盖字段
	if m := strings.ToUpper(strings.TrimSpace(a.Method)); m != "" {
		method = m
	}
	if u := strings.TrimSpace(a.URL); u != "" {
		rawURL = u
	}
	headers := map[string]string{}
	for k, v := range origHeaders {
		// 原始头整组带入（多值头用逗号合并：重放语义上等价，且 repeater 的头结构是单值）。
		headers[k] = strings.Join(v, ", ")
	}
	for k, v := range a.Headers {
		headers[k] = v // 覆盖或新增
	}
	for _, k := range a.RemoveHeaders {
		for existing := range headers {
			if strings.EqualFold(existing, strings.TrimSpace(k)) {
				delete(headers, existing)
			}
		}
	}
	body := string(origBody)
	if a.Body != nil {
		body = *a.Body
	}
	ver := strings.TrimSpace(a.HTTPVersion)
	if ver == "" {
		ver = httpVersion // 沿用原始协议
	}

	// 范围校验：这是 agent 侧最关键的一步。UI 重放由人负责授权判断，agent 重放
	// 必须由平台强制 —— 否则 worker 可以借重放打任何地址。
	taskID := agent.RunInfoFrom(ctx).TaskID
	matcher, merr := s.taskScopeMatcher(taskID)
	if merr != nil {
		return actool.Errorf(fmt.Sprintf("范围校验失败: %v", merr)), nil
	}
	if !matcher.InScope(rawURL) {
		return actool.Errorf(fmt.Sprintf(
			"目标 %q 不在本任务的授权范围内，已拒绝重放。请先通过 add_task_scope / add_company_scope 扩大授权范围",
			redactURLCreds(rawURL))), nil
	}

	useProxy := a.UseRecordingProxy
	if useProxy == nil {
		// 未显式指定时跟随实际状态：录制开着就经录制代理（顺带把这次重放也录成证据），
		// 关着就直连 —— 否则会连一个没在监听的端口。
		v := recording
		useProxy = &v
	}
	// 目标已在授权范围内 → 允许回环/链路本地（本地靶机、CTF 场景常见）。
	// 平台自身端口在 repeaterCheckURLScoped 里另有拦截，不会被重放命中。
	in := repeaterSendRequest{
		AllowLocal:         matcher.InScope(rawURL),
		Method:             &method,
		URL:                rawURL,
		Headers:            headers,
		Body:               body,
		TimeoutMs:          a.TimeoutMs,
		HTTPVersion:        ver,
		UseRecordingProxy:  useProxy,
		InsecureSkipVerify: a.InsecureSkipVerify,
	}
	resp, serr := s.executeRepeaterSend(ctx, in)
	if serr != nil {
		return actool.Errorf(fmt.Sprintf("重放失败: %v", serr)), nil
	}

	// 结果呈现：状态行 + 关键响应头 + 响应体预览。头只挑对判漏洞有意义的几个，
	// 全量头会把上下文撑满 —— 需要全量时用 traffic_get 看新录的那一条。
	var b strings.Builder
	b.WriteString(fmt.Sprintf("重放 %s %s\n", method, redactURLCreds(rawURL)))
	if resp.Error != "" {
		b.WriteString("传输层失败: " + resp.Error + "\n")
		return actool.Text(b.String()), nil
	}
	b.WriteString(fmt.Sprintf("响应: %d %s（%s，%dms，body %d 字节%s）\n",
		resp.Status, strings.TrimSpace(strings.TrimPrefix(resp.StatusText, fmt.Sprint(resp.Status))),
		resp.Proto, resp.ElapsedMs, resp.BodyBytes, truncNote(resp.BodyTruncated)))
	for _, h := range []string{"Content-Type", "Content-Length", "Location", "Set-Cookie", "Server", "X-Powered-By"} {
		if vs, ok := resp.Headers[h]; ok && len(vs) > 0 {
			v := strings.Join(vs, " | ")
			if h == "Set-Cookie" {
				v = redactCookieValues(v)
			}
			b.WriteString(fmt.Sprintf("%s: %s\n", h, clipRunes(v, 300)))
		}
	}
	if strings.TrimSpace(resp.Body) != "" {
		b.WriteString("\n--- 响应体（前 2000 字）---\n")
		b.WriteString(clipRunes(resp.Body, 2000))
	}
	return actool.Text(b.String()), nil
}

// parseRawRequestHead 解析录制下来的原始请求头块：
//
//	POST /api/orders?user_id=1 HTTP/1.1
//	Host: api.example.com
//	Cookie: a=b
//
// 返回头 map 与协议版本。Go 侧没有现成实现（前端在 repeater-http.ts 里自己解析），
// 这里按同样的语义写：首个冒号前是头名，之后整段是值（值里可含冒号）；
// 折行（以空格/制表符开头的续行）按 RFC 7230 拼到上一行。
func parseRawRequestHead(head string) (http.Header, string) {
	h := http.Header{}
	version := ""
	head = strings.ReplaceAll(head, "\r\n", "\n")
	lines := strings.Split(head, "\n")
	current := ""
	for i, ln := range lines {
		if i == 0 {
			// 请求行：METHOD SP target SP HTTP/x.y
			if f := strings.Fields(ln); len(f) >= 3 && strings.HasPrefix(f[len(f)-1], "HTTP/") {
				version = f[len(f)-1]
			}
			continue
		}
		if ln == "" {
			continue // 头块结束
		}
		if (strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t")) && current != "" {
			// 折行续接
			vals := h.Values(current)
			if len(vals) > 0 {
				h.Set(current, vals[len(vals)-1]+" "+strings.TrimSpace(ln))
			}
			continue
		}
		name, value, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		current = name
		h.Add(name, strings.TrimSpace(value))
	}
	// 协议版本规范成 repeater 认识的两个值。
	switch {
	case strings.HasPrefix(version, "HTTP/2"):
		version = "HTTP/2.0"
	case strings.HasPrefix(version, "HTTP/1."):
		version = "HTTP/1.1"
	default:
		version = ""
	}
	return h, version
}

// requestLineURL 从请求行的 target 里取 URL（代理录制时通常就是绝对 URL）。
func requestLineURL(head string) string {
	lines := strings.Split(strings.ReplaceAll(head, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}
	f := strings.Fields(lines[0])
	if len(f) < 2 {
		return ""
	}
	return f[1]
}

func truncNote(truncated bool) string {
	if truncated {
		return "，已截断"
	}
	return ""
}

// redactURLCreds 把 URL 里的 userinfo 抹掉再展示/报错 —— 凭据不该进模型上下文
// 或错误消息（用户名口令可能就在 http://user:pass@host 里）。
func redactURLCreds(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("***")
	return u.String()
}

// redactCookieValues 保留 Cookie 的名字、抹掉值：判"是否拿到了会话"只需要看
// 有没有 Set-Cookie 和它的属性，不需要把 token 明文塞进上下文。
func redactCookieValues(v string) string {
	parts := strings.Split(v, " | ")
	for i, p := range parts {
		if name, _, ok := strings.Cut(strings.TrimSpace(p), "="); ok {
			rest := ""
			if j := strings.Index(p, ";"); j >= 0 {
				rest = p[j:]
			}
			parts[i] = strings.TrimSpace(name) + "=***" + rest
		}
	}
	return strings.Join(parts, " | ")
}

// clipRunes 按字符（非字节）截断，避免把多字节字符切成半个。
func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// replayHeaderNames 返回一组稳定排序的头名，仅用于测试与调试输出。
func replayHeaderNames(h http.Header) []string {
	out := make([]string, 0, len(h))
	for k := range h {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
