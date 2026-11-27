package db

import "testing"

// TestFindingDedupKeySame 同类别+同资产+同端点（仅 scheme/端口/query 不同）必须同键。
// 越权/注入本来就是"同一个端点的不同参数"，query 不同不能算不同漏洞。
func TestFindingDedupKeySame(t *testing.T) {
	a := findingDedupKey("sqli", []int64{3, 1}, "curl http://a.com:80/api/orders?user_id=1 => 500")
	b := findingDedupKey("SQLi", []int64{1, 3, 3}, "GET https://a.com/api/orders?id=2 => 500")
	if a == "" || a != b {
		t.Fatalf("keys differ:\n a=%q\n b=%q", a, b)
	}
}

// TestFindingDedupKeyDifferent 任一分量不同就是不同键 —— 防止把真不同的漏洞并掉。
func TestFindingDedupKeyDifferent(t *testing.T) {
	base := findingDedupKey("sqli", []int64{1}, "http://a.com/api/orders")
	cases := map[string]string{
		"不同漏洞类别":  findingDedupKey("xss", []int64{1}, "http://a.com/api/orders"),
		"不同资产":    findingDedupKey("sqli", []int64{2}, "http://a.com/api/orders"),
		"不同端点":    findingDedupKey("sqli", []int64{1}, "http://a.com/api/users"),
		"不同主机":    findingDedupKey("sqli", []int64{1}, "http://b.com/api/orders"),
	}
	for name, v := range cases {
		if v == base {
			t.Errorf("%s: key collision with base (%q)", name, v)
		}
		if v == "" {
			t.Errorf("%s: empty key", name)
		}
	}
}

// TestFindingDedupKeySkips 无法判定时返回空串 —— 调用方据此跳过去重（宁可漏判）。
func TestFindingDedupKeySkips(t *testing.T) {
	cases := map[string]string{
		"无类别":   findingDedupKey("", []int64{1}, "http://a.com/x"),
		"无目标":   findingDedupKey("sqli", []int64{1}, "没有任何 URL 的纯文字描述"),
		"类别和目标都空": findingDedupKey("  ", nil, ""),
	}
	for name, v := range cases {
		if v != "" {
			t.Errorf("%s: want empty key, got %q", name, v)
		}
	}
}

// TestNormalizeDedupTarget 归一化只留 host+path，去掉 scheme/端口/query/尾斜杠。
func TestNormalizeDedupTarget(t *testing.T) {
	cases := map[string]string{
		"访问 http://A.com:8080/api/orders/?a=1 返回": "a.com/api/orders",
		"curl https://a.com/":                    "a.com",
	}
	for in, want := range cases {
		if got := normalizeDedupTarget(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
