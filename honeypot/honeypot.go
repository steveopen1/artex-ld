// Package honeypot 用静态签名识别「疑似蜜罐」资产，并给出置信度评分。
//
// 为什么需要：渗透任务里最浪费预算的情形之一，是把工具和注意力砸在一个
// 故意开放的诱捕目标上 —— 爆破了三小时弱口令、拿到了一个假 shell，回头
// 发现整条链是别人放的饵。这个判断在**资产落库时**就能做掉大半：蜜罐框架
// （Cowrie / OpenCanary / HFish / Glastopf / Dionaea）的默认指纹是公开且稳定的。
//
// 设计边界（很重要）：
//   - 只做**静态签名匹配**，不主动探测。落库时拿到的 title / 技术栈 / banner
//     已经够用，额外发包会打草惊蛇；
//   - 评分是概率，不是判决。所以对外只输出「分数 + 命中了哪些签名」，
//     由 agent/planner 决定怎么用；
//   - **全阴性不等于非蜜罐**：定制蜜罐没有公开指纹。反过来也成立 ——
//     一条签名命中也可能只是巧合。因此处置纪律按"信号数"分档（见 Classify）。
//
// 签名数据在 signatures.json，embed 进二进制，同时允许外部文件覆盖（热更新）：
// 蜜罐框架会改默认 banner，硬编码在 Go 里就意味着每次都要重新编译。
package honeypot

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed signatures.json
var embeddedSignatures []byte

// Asset 是参与匹配的资产视图 —— 只取与指纹相关的字段，避免包依赖 db。
type Asset struct {
	URL          string
	Host         string
	PageTitle    string
	Server       string // Server 响应头
	Technologies []string
	Banner       string // TCP banner / 原始响应片段
	RawHeaders   string // 拼接后的响应头文本（可选）
}

// Signature 是一条蜜罐指纹。
type Signature struct {
	ID      string `json:"id"`
	Name    string `json:"name"`    // 蜜罐框架/产品名
	Kind    string `json:"kind"`    // 匹配对象：title / server / technology / banner / header / url
	Pattern string `json:"pattern"` // 正则（大小写不敏感）
	Weight  int    `json:"weight"`  // 命中权重（1-100），默认 40
	Source  string `json:"source"`  // 依据来源，便于复核
}

// Hit 是一条命中的结果。
type Hit struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Field  string `json:"field"`
	Weight int    `json:"weight"`
	Source string `json:"source"`
}

// Verdict 是判定分档。
type Verdict string

const (
	// VerdictSuspected：≥2 条独立签名命中（或单条高权重）→ 视为确认疑似蜜罐。
	VerdictSuspected Verdict = "suspected"
	// VerdictWeak：仅 1 条低权重命中 → 只降级交互，不封锁方向。
	VerdictWeak Verdict = "weak"
	// VerdictClean：无命中。注意"clean"只代表没有公开指纹，不代表不是蜜罐。
	VerdictClean Verdict = "clean"
)

// Result 是对一个资产的完整判定。
type Result struct {
	Score    int     `json:"score"`   // 0-100
	Verdict  Verdict `json:"verdict"` // suspected / weak / clean
	Hits     []Hit   `json:"hits"`
	Evidence string  `json:"evidence,omitempty"` // 人类可读的一句话依据
}

var (
	loadOnce sync.Once
	sigs     []*regexp.Regexp
	metas    []Signature
	loadErr  error
)

// Reload 重新加载签名（外部文件优先，其次 embed）。
// 文件不存在不算错误 —— 用内置的一份即可。
func Reload(path string) error {
	// 不加 Once 保护：Reload 的语义就是"重新加载"，热更新需要能重复调用。
	// 首次自动加载由 ensureLoaded 负责。
	raw := embeddedSignatures
	if strings.TrimSpace(path) != "" {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			raw = b
		} else if err != nil && !os.IsNotExist(err) {
			loadErr = fmt.Errorf("读取蜜罐签名失败: %w", err)
			return loadErr
		}
	}
	var list []Signature
	if err := json.Unmarshal(raw, &list); err != nil {
		loadErr = fmt.Errorf("解析蜜罐签名失败: %w", err)
		return loadErr
	}
	compiled := make([]*regexp.Regexp, 0, len(list))
	kept := make([]Signature, 0, len(list))
	for _, s := range list {
		if strings.TrimSpace(s.Pattern) == "" || strings.TrimSpace(s.Kind) == "" {
			continue // 跳过无效条目而不是整份失败
		}
		re, err := regexp.Compile("(?i)" + s.Pattern)
		if err != nil {
			continue // 单条正则写错不该让整份签名库失效
		}
		if s.Weight <= 0 {
			s.Weight = 40
		}
		compiled = append(compiled, re)
		kept = append(kept, s)
	}
	sigs, metas, loadErr = compiled, kept, nil
	return nil
}

// ensureLoaded 保证内置签名至少加载过一次（外部覆盖由 Reload 显式触发）。
// 只认第一次：进程内重复解析没有意义，且 Reload 的显式调用仍然有效。
func ensureLoaded() {
	loadOnce.Do(func() {
		_ = Reload("")
	})
}

// Healthy 报告签名库是否可用（供启动自检）。
func Healthy() bool { return loadErr == nil && len(metas) > 0 }

// Count 返回已加载的签名条数。
func Count() int { return len(metas) }

// Classify 对一个资产做签名匹配与分档。
//
// 分档规则（对应"双信号才封锁、单信号只降级"的处置纪律）：
//   - suspected：≥2 条命中，或单条权重 ≥80（极强的默认指纹，如 Cowrie 的
//     特定 SSH banner）→ 建议回避，不投递、不爆破、不利用；
//   - weak：仅 1 条低权重命中 → 只降低交互强度（不打高强度字典、不做破坏性利用），
//     但不封锁整个方向（可能只是巧合）；
//   - clean：无命中。**不代表安全**，只代表没有公开指纹。
func Classify(a Asset) Result {
	// 惰性加载：签名库必须"任何入口都可用"。之前只在显式 Reload 后才生效，
	// 而没有任何启动路径调用它 —— 结果就是 Classify 永远返回 clean，
	// 蜜罐识别静默失效（集成测试抓到的就是这个）。
	ensureLoaded()
	if sigs == nil {
		return Result{Verdict: VerdictClean}
	}
	fields := map[string]string{
		"title":      a.PageTitle,
		"server":     a.Server,
		"banner":     a.Banner,
		"header":     a.RawHeaders,
		"url":        a.URL,
		"host":       a.Host,
		"technology": strings.Join(a.Technologies, " "),
	}
	var hits []Hit
	total := 0
	for i, re := range sigs {
		m := metas[i]
		val := fields[m.Kind]
		if strings.TrimSpace(val) == "" || !re.MatchString(val) {
			continue
		}
		hits = append(hits, Hit{ID: m.ID, Name: m.Name, Kind: m.Kind, Field: m.Kind, Weight: m.Weight, Source: m.Source})
		total += m.Weight
	}
	if total > 100 {
		total = 100
	}
	res := Result{Score: total, Hits: hits}
	// 同一框架的多条签名（例如 Cowrie 同时命中 title 和 banner）算"一个信号源"，
	// 否则一个框架写三条签名就会自动升级成 suspected —— 那是签名库的写法问题，
	// 不是资产的可疑程度。
	independent := independentNames(hits)
	switch {
	case len(independent) >= 2 || (len(hits) >= 1 && hits[0].Weight >= 80):
		res.Verdict = VerdictSuspected
	case len(hits) >= 1:
		res.Verdict = VerdictWeak
	default:
		res.Verdict = VerdictClean
	}
	if len(hits) > 0 {
		names := make([]string, 0, len(independent))
		for n := range independent {
			names = append(names, n)
		}
		sort.Strings(names)
		res.Evidence = fmt.Sprintf("命中 %d 条签名（%s），置信度 %d", len(hits), strings.Join(names, "、"), total)
	}
	return res
}

func independentNames(hits []Hit) map[string]bool {
	m := map[string]bool{}
	for _, h := range hits {
		if h.Name != "" {
			m[h.Name] = true
		} else {
			m[h.ID] = true
		}
	}
	return m
}

// ShouldAvoid 在 Verdict 为 suspected 时为 true —— 调用方据此把该资产
// 标成"回避"：不投递、不爆破、不利用。
func (r Result) ShouldAvoid() bool { return r.Verdict == VerdictSuspected }
