// finding_dedup.go：findings 写入层的机械查重键。
//
// 这是"去重四层流水线"的前两层（写入扩展键 → 数据库兜底查重）。后两层
// （审核阶段语义判重、人工合并）已经存在，但它们都发生在**事后**：重复条目
// 已经堆进列表，报告侧得靠人工并。前两层要解决的是"同一件事被重复上报"这一
// 最常见的情形 —— 同一个 worker 换措辞再报一次、两个 worker 撞到同一点。
//
// 键的构成（越窄越安全）：
//
//	sha256( vulnclass | 排序去重后的 asset_ids | 归一化目标 )
//
// 三个部分缺一不可：只有 vulnclass 会把"同一个站点的两个不同注入点"并成一条；
// 只有目标会把"同一路径的 SQLi 和 XSS"并成一条。
//
// 归一化目标是关键：从 evidence 里抽第一个 URL/主机，剥掉 scheme、端口、
// query 与末尾斜杠，只留 host+path。这样
//   http://a.com:80/api/orders?user_id=1  和  https://a.com/api/orders?id=2
// 会被认定为同一个端点 —— 而越权/注入本来就是"同一个端点的不同参数"。
//
// 判定不出目标时返回空串：调用方据此**跳过**去重（不猜）。宁可漏判，
// 不可把两个真不同的漏洞并掉 —— 误合会让报告少报一个漏洞，代价比多报一条高。
package db

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"artex/scopetext"
)

// findingDedupKey 计算一条 finding 的去重键。返回 "" 表示无法判定（不去重）。
func findingDedupKey(vulnClass string, assetIDs []int64, evidence string) string {
	class := strings.ToLower(strings.TrimSpace(vulnClass))
	if class == "" {
		return "" // 没有漏洞类别就无法可靠判同
	}
	target := normalizeDedupTarget(evidence)
	if target == "" {
		return "" // 没有端点信息，跳过（不猜）
	}
	ids := make([]int64, 0, len(assetIDs))
	seen := map[int64]bool{}
	for _, id := range assetIDs {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var b strings.Builder
	b.WriteString(class)
	b.WriteByte('|')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatInt(id, 10))
	}
	b.WriteByte('|')
	b.WriteString(target)

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:16]) // 16 字节足够，碰撞概率可忽略
}

// normalizeDedupTarget 从证据文本里取第一个网络目标并归一化成 host+path。
//
// 用 scopetext.Targets 而不是另写一套提取：那个包已经把"什么算高置信度目标"
// 的边界定义清楚了（URL / IP / 网络工具上下文下的主机名），复用可以保证
// 去重口径与范围校验口径一致 —— 两处不一致会让"同一个目标"在一边被识别、
// 在另一边被忽略，很难解释。
func normalizeDedupTarget(evidence string) string {
	targets := scopetext.Targets(evidence)
	if len(targets) == 0 {
		return ""
	}
	t := targets[0]
	host := scopetext.HostOfURL(t)
	if host == "" {
		return ""
	}
	path := scopetext.PathOfURL(t)
	path = strings.TrimRight(path, "/")
	return strings.ToLower(host) + path
}

// DedupDiagnostics 是给测试与调试用的可读描述（生产路径不调用）。
func DedupDiagnostics(vulnClass string, assetIDs []int64, evidence string) string {
	return fmt.Sprintf("class=%q target=%q key=%q",
		vulnClass, normalizeDedupTarget(evidence), findingDedupKey(vulnClass, assetIDs, evidence))
}

// DuplicateNodeIDs 返回本任务里被写入层去重标成 duplicate 的 finding 节点 id 集合。
//
// 报告侧据此把这些条目排除在交付清单之外：它们已经以"初次上报"那条的形式
// 出现在报告里，再列一遍会让同一个漏洞在报告里出现两次 —— 这是客户侧最难解释
// 的错误（会让对方怀疑整份报告的可信度）。人工若把 status 改回来，这里自然
// 就不再排除。
func (s *ExplorationStore) DuplicateNodeIDs() (map[int64]bool, error) {
	rows, err := s.db.Query(`SELECT node_id FROM findings
WHERE status='duplicate' AND node_id IS NOT NULL AND task_id IN (
  SELECT id FROM tasks WHERE exploration_id=$1
)`, s.expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
