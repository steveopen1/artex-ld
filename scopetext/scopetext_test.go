package scopetext

import (
	"reflect"
	"testing"
)

// TestTargetsURL 绝对 URL 是最高置信度来源，任何命令形态都要能取出来。
func TestTargetsURL(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"curl https://api.example.com/v1/orders?user_id=1", []string{"https://api.example.com/v1/orders?user_id=1"}},
		{`curl -X POST "https://a.com/x" -d '{"k":1}'`, []string{"https://a.com/x"}},
		{"curl -s http://10.0.0.1:8080/admin | grep flag", []string{"http://10.0.0.1:8080/admin"}},
		{"wget http://a.com/1.zip && unzip 1.zip", []string{"http://a.com/1.zip"}},
	}
	for _, c := range cases {
		if got := Targets(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Targets(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

// TestTargetsIPAndCIDR 裸 IP / CIDR / IP:port 必须能取出 —— 内网段扫描最常用这些形态，
// 而它们不带 scheme，URL 正则抓不到。
func TestTargetsIPAndCIDR(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"nmap -sV 10.0.0.0/24", []string{"10.0.0.0/24"}},
		{"nmap -p 80 192.168.1.10", []string{"192.168.1.10"}},
		{"curl http://10.1.1.1:8080/", []string{"http://10.1.1.1:8080/"}},
		{"nc -zv 172.16.0.5 22", []string{"172.16.0.5"}},
		{"fscan -h 10.0.0.1,10.0.0.2", []string{"10.0.0.1", "10.0.0.2"}},
	}
	for _, c := range cases {
		if got := Targets(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Targets(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

// TestTargetsBareHostRequiresNetworkTool 裸主机名只在命令确实调用网络工具时才采信。
// 这条是"不误杀正常命令"的关键：否则 `python3 script.py` 里的 script.py 会被当域名。
func TestTargetsBareHostRequiresNetworkTool(t *testing.T) {
	// 网络工具 → 采信
	if got := Targets("httpx -u api.example.com"); !contains(got, "api.example.com") {
		t.Fatalf("network tool host not captured: %v", got)
	}
	// 非网络工具 → 不采信（脚本文件名不能被当成目标）
	for _, cmd := range []string{
		"python3 script.py",
		"bash run.sh",
		"cat config.yaml",
		"go test ./pkg",
	} {
		if got := Targets(cmd); len(got) != 0 {
			t.Errorf("Targets(%q) = %v, want empty (must not treat filenames as targets)", cmd, got)
		}
	}
}

// TestTargetsSkipsDataFiles 即使上下文里是网络工具，字典/模板文件也不能当目标。
func TestTargetsSkipsDataFiles(t *testing.T) {
	got := Targets("nuclei -u api.example.com -t sqli.yaml -o out.txt")
	if !contains(got, "api.example.com") {
		t.Fatalf("target missing: %v", got)
	}
	for _, bad := range []string{"sqli.yaml", "out.txt"} {
		if contains(got, bad) {
			t.Errorf("%q must not be treated as a target: %v", bad, got)
		}
	}
}

// TestTargetsNoTargets 解析不出目标时返回空 —— 调用方据此放行并记审计。
func TestTargetsNoTargets(t *testing.T) {
	for _, cmd := range []string{"", "ls -la", "cat /etc/passwd", "echo hello"} {
		if got := Targets(cmd); len(got) != 0 {
			t.Errorf("Targets(%q) = %v, want empty", cmd, got)
		}
	}
}

// TestTargetsDedup 同一目标出现多次只报一次。
func TestTargetsDedup(t *testing.T) {
	got := Targets("curl http://a.com/x; curl http://a.com/x")
	if len(got) != 1 || got[0] != "http://a.com/x" {
		t.Fatalf("dedup failed: %v", got)
	}
}

// TestTargetsInvalidIPv4 版本号形态不能被当成 IPv4。
func TestTargetsInvalidIPv4(t *testing.T) {
	if got := Targets("curl https://a.com/v999.999.999.999"); contains(got, "999.999.999.999") {
		t.Fatalf("invalid ipv4 accepted: %v", got)
	}
}

// TestHostOfURLAndPath 两个辅助函数保持既有语义（被 server 侧复用）。
func TestHostOfURLAndPath(t *testing.T) {
	cases := []struct{ url, host, path string }{
		{"https://a.example.com:8443/x/y?z=1", "a.example.com", "/x/y"},
		{"http://user:pw@b.com/", "b.com", ""},
		{"https://c.com", "c.com", ""},
	}
	for _, c := range cases {
		if got := HostOfURL(c.url); got != c.host {
			t.Errorf("HostOfURL(%q) = %q, want %q", c.url, got, c.host)
		}
		if got := PathOfURL(c.url); got != c.path {
			t.Errorf("PathOfURL(%q) = %q, want %q", c.url, got, c.path)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestTargetsShellVariableInURL 回归：实测踩过 —— `curl "http://127.0.0.1:8811$p"`
// 若不排除 shell 变量符，会提取出带 $p 的非法 URL，ScopeMatcher 判 false，
// 把**范围内**的合法命令误拦。这里锁住"必须在 $ 处截断"。
func TestTargetsShellVariableInURL(t *testing.T) {
	got := Targets(`curl -s "http://127.0.0.1:8811$p" -o /tmp/x`)
	if len(got) != 1 || got[0] != "http://127.0.0.1:8811" {
		t.Fatalf("Targets = %v, want [http://127.0.0.1:8811]", got)
	}
	// 反引号命令替换同理
	got = Targets("curl http://a.com/`id`")
	if len(got) != 1 || got[0] != "http://a.com/" {
		t.Fatalf("backtick: %v", got)
	}
	// 花括号展开同理
	got = Targets("curl http://a.com/{a,b}")
	if len(got) != 1 || got[0] != "http://a.com/" {
		t.Fatalf("brace: %v", got)
	}
}
