package proxyauth

import (
	"encoding/base64"
	"net/http"
	"testing"
)

// TestTokenAndVerify 令牌必须能往返校验，且换任务/换密钥都失效。
func TestTokenAndVerify(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	tok := Token(secret, 7)
	if !Verify(secret, 7, tok) {
		t.Fatal("valid token rejected")
	}
	if Verify(secret, 8, tok) {
		t.Fatal("token accepted for a different task")
	}
	if Verify([]byte("another-secret-another-secret-32"), 7, tok) {
		t.Fatal("token accepted under a different secret")
	}
	if Verify(secret, 7, "") {
		t.Fatal("empty token accepted")
	}
}

// TestParse 能解出 userinfo 里的 taskID 与令牌。
func TestParse(t *testing.T) {
	secret := []byte("k")
	user, pass := UserAndPass(secret, 42)
	header := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))

	id, tok, ok := Parse(header)
	if !ok || id != 42 || tok != pass {
		t.Fatalf("Parse = (%d,%q,%v)", id, tok, ok)
	}
}

// TestParseRejects 非本平台形态一律 ok=false（调用方据此按"无归属流量"处理）。
func TestParseRejects(t *testing.T) {
	cases := []string{
		"",
		"Bearer abc",
		"Basic " + base64.StdEncoding.EncodeToString([]byte("someone:pass")),
		"Basic " + base64.StdEncoding.EncodeToString([]byte("task-abc:x")),
		"Basic " + base64.StdEncoding.EncodeToString([]byte("task--1:x")),
		"Basic !!!notbase64!!!",
	}
	for _, c := range cases {
		if _, _, ok := Parse(c); ok {
			t.Errorf("Parse(%q) accepted, want reject", c)
		}
	}
}

// TestStripHeader 代理凭证绝不能泄漏到目标服务器。
func TestStripHeader(t *testing.T) {
	h := http.Header{}
	h.Set("Proxy-Authorization", "Basic x")
	h.Set("proxy-authorization", "Basic y") // 大小写变体
	h.Set("Authorization", "Bearer keep")
	StripHeader(h)
	if len(h.Values("Proxy-Authorization")) != 0 {
		t.Fatal("Proxy-Authorization not stripped")
	}
	if h.Get("Authorization") != "Bearer keep" {
		t.Fatal("Authorization must be preserved (it's the target's own auth)")
	}
}
