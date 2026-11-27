// Package proxyauth 给「代理流量」做任务归属。
//
// 为什么要归属：代理是全局单例，一条 TCP 连接进来时它不知道这是哪个任务发出的
// 流量，因此无法按任务授权范围拒绝。文本层的范围校验（scopetext + Guard）能被
// 脚本、管道、base64 绕过 —— 代理层是网络级的最后一道，但它必须知道"这流量属于谁"。
//
// 做法：给每个任务签发一个代理凭证，格式是 HTTP Basic 的 userinfo：
//
//	http://task-<id>:<hmac>@127.0.0.1:<port>
//
// 代理从 Proxy-Authorization 里解出 taskID 并校验 HMAC，然后按该任务的
// ScopeMatcher 判定目标主机。token 用服务进程的随机密钥做 HMAC-SHA256，
// 进程重启即失效（不需要持久化：代理地址本身也是启动时才知道的）。
package proxyauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// UserPrefix 是代理凭证 user 部分的固定前缀。
const UserPrefix = "task-"

// Token 计算某任务的代理令牌（hex 编码的 HMAC-SHA256 前缀）。
// 截断到 16 字节足够防伪造，又让 URL 不至于太长（它会进环境变量）。
func Token(secret []byte, taskID int64) string {
	m := hmac.New(sha256.New, secret)
	fmt.Fprintf(m, "proxy-task:%d", taskID)
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// UserAndPass 返回代理 URL userinfo 的两段：user 是 "task-<id>"，pass 是令牌。
// 必须分开给 url.UserPassword，否则冒号会被当成密码分隔符又被拼一遍。
func UserAndPass(secret []byte, taskID int64) (string, string) {
	return fmt.Sprintf("%s%d", UserPrefix, taskID), Token(secret, taskID)
}

// UserInfo 返回可读的 "task-<id>:<token>"（用于日志/自检，不直接塞进 URL）。
func UserInfo(secret []byte, taskID int64) string {
	u, p := UserAndPass(secret, taskID)
	return u + ":" + p
}

// Parse 从 Proxy-Authorization 头解析出 (taskID, token)。
// ok=false 表示头缺失/格式不对/不是本平台签发的形态 —— 调用方据此决定
// 是"无归属流量"还是"伪造凭证"。
func Parse(header string) (taskID int64, token string, ok bool) {
	h := strings.TrimSpace(header)
	if h == "" {
		return 0, "", false
	}
	// 只接受 Basic
	if len(h) < 6 || !strings.EqualFold(h[:6], "basic ") {
		return 0, "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[6:]))
	if err != nil {
		return 0, "", false
	}
	user, pass, found := strings.Cut(string(raw), ":")
	if !found {
		return 0, "", false
	}
	if !strings.HasPrefix(user, UserPrefix) {
		return 0, "", false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(user, UserPrefix), 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	return id, pass, true
}

// Verify 校验 token 是否与签发的一致（常量时间比较，避免时序侧信道）。
func Verify(secret []byte, taskID int64, token string) bool {
	want := Token(secret, taskID)
	return subtle.ConstantTimeCompare([]byte(want), []byte(token)) == 1
}

// StripHeader 删除请求里的 Proxy-Authorization。
// 必须在把请求转发到目标之前调用：这个头只给代理看，绝不能泄漏到目标服务器。
func StripHeader(h http.Header) {
	if h == nil {
		return
	}
	for k := range h {
		if strings.EqualFold(k, "Proxy-Authorization") {
			h.Del(k)
		}
	}
}
