// dedident_migration.go：把历史版本里带产品名（artex）的提示词正文去标识化。
//
// 为什么需要单独的迁移，而不是靠 reseed 覆盖默认值：
//   reseed 的语义是"把提示词重置成代码默认"，会**覆盖用户的编辑**。而这里要做的
//   只是一次等价替换（artex → 本平台），用户对提示词的其它改动必须原样保留。
//   所以走 UPDATE ... REPLACE 而不是 ResetPromptToDefault。
//
// 为什么用一次性开关：迁移本身幂等（替换过的文本再替换一次无变化），但没有必要
// 每次启动都全表扫一遍。
package server

import (
	"log"
	"strings"
)

// dedidentPromptFlag 是一次性迁移标记。
const dedidentPromptFlag = "prompt_deident_migration_v1"

// migrateDeidentifyPrompts 把 agent_prompts 里残留的产品名替换成中性表述。
// 只处理出现过的两种形态，避免误伤用户提示词里恰好叫这个名字的目标资产。
func (s *Server) migrateDeidentifyPrompts() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	if v, _, _ := s.m.pg.GetSetting(dedidentPromptFlag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(dedidentPromptFlag, "true") }()

	pairs := []struct{ from, to string }{
		{"你是 artex 的", "你是本平台的"},
		{"你是 ARTEX 的", "你是本平台的"},
		{"ARTEX_WORKER_CHAT", "WORKER_CHAT"},
		{"【artex 平台管控", "【平台管控"},
	}
	rows, err := s.m.pg.Query(`SELECT id, template_text FROM agent_prompts
WHERE template_text ILIKE '%artex%'`)
	if err != nil {
		log.Printf("[deident] 读取提示词失败: %v", err)
		return
	}
	type hit struct {
		id   int64
		text string
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.text); err != nil {
			rows.Close()
			return
		}
		hits = append(hits, h)
	}
	rows.Close()
	for _, h := range hits {
		next := h.text
		for _, p := range pairs {
			next = strings.ReplaceAll(next, p.from, p.to)
		}
		if next == h.text {
			continue // 命中 ILIKE 但没有可替换的形态：不写库，避免无意义的版本变更
		}
		if _, err := s.m.pg.Exec(`UPDATE agent_prompts SET template_text=$2, updated_by='deident' WHERE id=$1`, h.id, next); err != nil {
			log.Printf("[deident] 更新提示词 %d 失败: %v", h.id, err)
			continue
		}
		log.Printf("[deident] 提示词 %d 已去标识化", h.id)
	}
}
