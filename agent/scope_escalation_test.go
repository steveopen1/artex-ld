package agent

import (
	"testing"

	"artex/db"
)

// TestAgentScopeWithinCurrent 锁住"agent 只能写细、不能扩大"的规则。
// 来源是一次实测：agent 被范围校验拦下后，调 add_task_scope 把 example.com
// 加进 scope 自我授权，拦截随即失效。
func TestAgentScopeWithinCurrent(t *testing.T) {
	cur := db.BuildScopeMatcher([]db.TaskScope{
		{Kind: "root_domain", Domain: "example.com"},
		{Kind: "cidr", Net: "10.0.0.0/24"},
	}, nil)
	if !cur.HasAny {
		t.Fatal("fixture 失效：matcher 应有内容")
	}

	allow := []struct{ kind, value string }{
		{"subdomain", "api.example.com"}, // 已授权根域下的子域 = 细化
		{"subdomain", "example.com"},     // 根域本身也在范围内
		{"ip", "10.0.0.5"},               // 落在已授权网段
		{"cidr", "10.0.0.0/28"},          // 更小的子网 = 细化
	}
	for _, c := range allow {
		if !agentScopeWithinCurrent(cur, c.kind, c.value) {
			t.Errorf("%s=%s 应允许（属于细化）", c.kind, c.value)
		}
	}

	deny := []struct{ kind, value string }{
		{"root_domain", "evil.com"},      // 全新根域 = 扩大
		{"subdomain", "api.evil.com"},    // 不属于已授权根域
		{"ip", "8.8.8.8"},                // 不在已授权网段
		{"cidr", "0.0.0.0/0"},            // 试图覆盖全网
		{"company", "某公司"},              // 按公司整体扩大
		{"icp", "京ICP备12345"},           // 按备案号扩大
		{"keyword", "某集团"},              // 按关键词扩大
	}
	for _, c := range deny {
		if agentScopeWithinCurrent(cur, c.kind, c.value) {
			t.Errorf("%s=%s 应拒绝（属于扩大授权）", c.kind, c.value)
		}
	}

	// 任务本身没有任何范围时，agent 不能凭空创建授权
	empty := db.EmptyScopeMatcher()
	if agentScopeWithinCurrent(empty, "subdomain", "a.example.com") {
		t.Error("空范围下 agent 不应能添加任何目标")
	}
}
