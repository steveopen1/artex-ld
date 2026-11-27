package server

import "testing"

// TestParseRawRequestHead 锁住 req_head 解析的语义：多值头、折行续接、协议版本。
// http_replay 的"沿用原始请求"完全依赖这个解析 —— 它错了，重放出来的就不是
// 同一条请求，而这正是本工具存在的理由。
func TestParseRawRequestHead(t *testing.T) {
	head := "POST /api/orders?user_id=1 HTTP/1.1\r\n" +
		"Host: api.example.com\r\n" +
		"Cookie: sid=abc; theme=dark\r\n" +
		"X-Multi: first\r\n" +
		"X-Multi: second\r\n" +
		"X-Folded: part1\r\n" +
		" part2\r\n" +
		"Content-Type: application/json\r\n"

	h, ver := parseRawRequestHead(head)
	if ver != "HTTP/1.1" {
		t.Fatalf("version = %q, want HTTP/1.1", ver)
	}
	if got := h.Get("Host"); got != "api.example.com" {
		t.Fatalf("Host = %q", got)
	}
	if got := h.Get("Cookie"); got != "sid=abc; theme=dark" {
		t.Fatalf("Cookie = %q", got)
	}
	// 多值头必须都保留（values 顺序按出现序）
	if got := h.Values("X-Multi"); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("X-Multi = %v", got)
	}
	// 折行续接：值里含冒号不应被当成新头
	if got := h.Get("X-Folded"); got != "part1 part2" {
		t.Fatalf("X-Folded = %q, want %q", got, "part1 part2")
	}
	if got := h.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
}

// TestParseRawRequestHeadHTTP2 协议版本要归一成 repeater 认识的两个值：
// 原始报文可能是 h2 / HTTP/2，但 repeaterSendRequest.HTTPVersion 只认 HTTP/2.0。
func TestParseRawRequestHeadHTTP2(t *testing.T) {
	for _, in := range []string{
		"GET / HTTP/2\r\nHost: a\r\n",
		"GET / HTTP/2.0\r\nHost: a\r\n",
	} {
		_, ver := parseRawRequestHead(in)
		if ver != "HTTP/2.0" {
			t.Fatalf("%q -> version %q, want HTTP/2.0", in, ver)
		}
	}
	// 空头块不应 panic，版本为空
	if _, ver := parseRawRequestHead(""); ver != "" {
		t.Fatalf("empty head -> version %q, want empty", ver)
	}
}

// TestRequestLineURL 请求行的 target 要能取出来（代理录制时是绝对 URL，
// 源站录制时是 path，调用方需要区分这两种情况）。
func TestRequestLineURL(t *testing.T) {
	// 代理录制时 target 是绝对 URL；源站录制时是 path —— 调用方据此决定要不要补 host。
	if got := requestLineURL("GET http://a.com/x HTTP/1.1\r\n"); got != "http://a.com/x" {
		t.Fatalf("absolute target = %q", got)
	}
	if got := requestLineURL("GET /x?y=1 HTTP/1.1\r\nHost: a\r\n"); got != "/x?y=1" {
		t.Fatalf("path target = %q", got)
	}
	if got := requestLineURL(""); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if got := requestLineURL("garbage"); got != "" {
		t.Fatalf("garbage = %q", got)
	}
}

// TestRedactURLCreds URL 里的 userinfo 不能进模型上下文。
func TestRedactURLCreds(t *testing.T) {
	got := redactURLCreds("http://user:secret@example.com/a?b=1")
	if got != "http://%2A%2A%2A@example.com/a?b=1" && got != "http://***@example.com/a?b=1" {
		t.Fatalf("redacted = %q", got)
	}
	if redactURLCreds("http://example.com/a") != "http://example.com/a" {
		t.Fatal("URL without userinfo must be unchanged")
	}
}

// TestRedactCookieValues 保留 cookie 名与属性、抹掉值（判会话只需要看有没有，不需要明文）。
func TestRedactCookieValues(t *testing.T) {
	got := redactCookieValues("sid=abc123; Path=/; HttpOnly | theme=dark")
	if got != "sid=***; Path=/; HttpOnly | theme=***" {
		t.Fatalf("cookies = %q", got)
	}
}

// TestClipRunes 按字符截断，不能把多字节字符切半。
func TestClipRunes(t *testing.T) {
	if got := clipRunes("中文测试", 2); got != "中文…" {
		t.Fatalf("got %q", got)
	}
	if got := clipRunes("abc", 10); got != "abc" {
		t.Fatalf("got %q", got)
	}
}
