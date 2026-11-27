package honeypot

import "testing"

// TestReloadAndCount 内置签名必须能加载（embed 的 JSON 是编译期依赖，
// 写错了就是启动即坏）。
func TestReloadAndCount(t *testing.T) {
	if err := Reload(""); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !Healthy() {
		t.Fatal("signature set unhealthy")
	}
	if Count() < 10 {
		t.Fatalf("only %d signatures loaded", Count())
	}
}

// TestClassifySuspected 两个不同框架各命中一条 → suspected（双信号才封锁）。
func TestClassifySuspected(t *testing.T) {
	_ = Reload("")
	r := Classify(Asset{
		PageTitle: "Cowrie",
		Server:    "OpenCanary",
	})
	if !r.ShouldAvoid() || r.Verdict != VerdictSuspected {
		t.Fatalf("verdict=%s score=%d hits=%d", r.Verdict, r.Score, len(r.Hits))
	}
}

// TestClassifyWeakLowWeight 单条低权重命中只判 weak —— 不封锁方向，只降级交互。
// 这是"单信号只降级"的一半；另一半是"高权重单条即 suspected"（见下个用例）。
func TestClassifyWeakLowWeight(t *testing.T) {
	_ = Reload("")
	r := Classify(Asset{Banner: "root@svr04:~#"}) // Cowrie 伪 shell 提示符，权重 50
	if r.Verdict != VerdictWeak {
		t.Fatalf("low-weight single hit must stay weak, got %s (score %d)", r.Verdict, r.Score)
	}
	if r.ShouldAvoid() {
		t.Fatal("weak verdict must not trigger avoid")
	}
}

// TestClassifyClean 无命中 → clean，且必须**不**被当作"安全"结论使用。
func TestClassifyClean(t *testing.T) {
	_ = Reload("")
	r := Classify(Asset{PageTitle: "Welcome to nginx", Server: "nginx/1.24.0"})
	if r.Verdict != VerdictClean || r.Score != 0 || r.ShouldAvoid() {
		t.Fatalf("verdict=%s score=%d", r.Verdict, r.Score)
	}
}

// TestClassifyHighWeightSingle 单条权重 ≥80 视为强指纹，直接 suspected。
func TestClassifyHighWeightSingle(t *testing.T) {
	_ = Reload("")
	r := Classify(Asset{Banner: "SSH-2.0-OpenSSH_6.0p1 Debian-4+deb7u2"})
	if r.Verdict != VerdictSuspected {
		t.Fatalf("high-weight single hit must be suspected, got %s (score %d)", r.Verdict, r.Score)
	}
}

// TestScoreCapped 多条命中时分数封顶 100，不越界。
func TestScoreCapped(t *testing.T) {
	_ = Reload("")
	r := Classify(Asset{
		PageTitle: "Cowrie", Server: "Canary", Banner: "Kippo", Technologies: []string{"hfish"},
	})
	if r.Score > 100 {
		t.Fatalf("score %d > 100", r.Score)
	}
}
