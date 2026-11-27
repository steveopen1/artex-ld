package tool

import (
	"strings"
	"testing"
)

// TestDenyChildEnv 敏感/可识别变量必须被拦住。
// 这条测试的直接来源是一次实测：agent 跑 `env` 就能读到平台的 DSN 变量，
// 里面既有产品名也有数据库口令。
func TestDenyChildEnv(t *testing.T) {
	deny := []string{
		"ARTEX_PG_DSN",
		"ARTEX_LLM_API_KEY",
		"ZCODE_OFFICIAL_ENDPOINT_TOKEN",
		"OPENAI_API_KEY",
		"DATABASE_PG_DSN",
		"MY_SECRET",
		"GITHUB_TOKEN",
		"DB_PASSWORD",
	}
	for _, k := range deny {
		if !denyChildEnv(k) {
			t.Errorf("%s 应被过滤", k)
		}
	}
	allow := []string{
		"PATH", "HOME", "LANG", "TERM", "HTTP_PROXY", "HTTPS_PROXY",
		"SSL_CERT_FILE", "CURL_CA_BUNDLE", "TMPDIR", "PWD", "USER",
	}
	for _, k := range allow {
		if denyChildEnv(k) {
			t.Errorf("%s 不该被过滤（子进程正常工作依赖它）", k)
		}
	}
}

// TestChildEnvAlwaysExplicit childEnv 必须返回显式切片而不是 nil ——
// 返回 nil 会让子进程继承完整环境，正是要修的那个洞。
func TestChildEnvAlwaysExplicit(t *testing.T) {
	got := childEnv(nil)
	if got == nil {
		t.Fatal("childEnv 不能返回 nil（nil 意味着继承全部环境）")
	}
	if len(got) == 0 {
		t.Fatal("至少应保留 PATH 等基础变量")
	}
	for _, kv := range got {
		name, _, _ := strings.Cut(kv, "=")
		if denyChildEnv(name) {
			t.Errorf("输出里仍有敏感变量: %s", name)
		}
	}
}

// TestChildEnvExtraOverrides extra 显式传入时不过滤（由调用方负责），且排在最后。
func TestChildEnvExtraOverrides(t *testing.T) {
	got := childEnv([]string{"ARTEX_EXPLICIT=1"})
	if len(got) == 0 || got[len(got)-1] != "ARTEX_EXPLICIT=1" {
		t.Fatalf("extra 应在末尾: %v", got[len(got)-1])
	}
}
