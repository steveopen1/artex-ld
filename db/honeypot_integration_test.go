package db

import (
	"os"
	"testing"
)

// TestUpsertHTTPServiceComputesHoneypotScore 走真实落库路径，确认蜜罐评分
// 被写进 assets 行 —— 这是"资产落库时匹配签名"这条链路的唯一验证点。
// 需要真实 PG（与其它集成测试同样的 DSN 约定）。
func TestUpsertHTTPServiceComputesHoneypotScore(t *testing.T) {
	dsn := os.Getenv("ARTEX_PG_DSN")
	if dsn == "" {
		t.Skip("ARTEX_PG_DSN 未设置，跳过集成测试")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	as := d.Assets()
	url := "http://cowrie-honeypot-check.test/hfish"
	if _, err := as.UpsertHTTPService(UpsertHTTPServiceReq{
		URL:         url,
		PageTitle:   "HFish 蜜罐管理",
		Technologies: []string{"glastopf"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var score float64
	var ev string
	if err := d.QueryRow(`SELECT honeypot_score, honeypot_evidence::text FROM assets WHERE url=$1`, url).Scan(&score, &ev); err != nil {
		t.Fatalf("query: %v", err)
	}
	if score < 80 {
		t.Fatalf("honeypot_score = %v, want >=80 (title+tech 双信号)；evidence=%s", score, ev)
	}
	if ev == "" || ev == "{}" {
		t.Fatalf("honeypot_evidence 为空")
	}

	// 正常站点不该被误判。用返回的 id 查 —— URL 会被 normalizeURL 归一化
	// （去尾斜杠等），按原始串查不到。
	normalID, err := as.UpsertHTTPService(UpsertHTTPServiceReq{URL: "http://normal-site-check.test/", PageTitle: "Welcome to nginx"})
	if err != nil {
		t.Fatalf("upsert normal: %v", err)
	}
	var nscore float64
	if err := d.QueryRow(`SELECT honeypot_score FROM assets WHERE id=$1`, normalID).Scan(&nscore); err != nil {
		t.Fatalf("query normal: %v", err)
	}
	if nscore != 0 {
		t.Fatalf("普通站点被误判为蜜罐：score=%v", nscore)
	}

	var honeypotID int64
	_ = d.QueryRow(`SELECT id FROM assets WHERE url=$1`, url).Scan(&honeypotID)
	// 清理
	_, _ = d.Exec(`DELETE FROM assets WHERE id IN ($1,$2)`, honeypotID, normalID)
}
