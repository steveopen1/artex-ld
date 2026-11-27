package db

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

// TestRecordFindingTxMarksDuplicate 走真实 RecordFindingTx，确认：
//   - 第一次上报正常落库（status=pending，DuplicateOf=0）；
//   - 第二次同类别+同资产+同端点（仅 query 不同）被标记 duplicate 并指向第一条。
// 这是"写入层去重"这一层唯一的验证点 —— 逻辑写错的话，重复条目会静默流进报告。
func TestRecordFindingTxMarksDuplicate(t *testing.T) {
	dsn := os.Getenv("ARTEX_PG_DSN")
	if dsn == "" {
		t.Skip("ARTEX_PG_DSN 未设置，跳过集成测试")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	ctx := context.Background()
	// 先建 exploration，再建 task（tasks.exploration_id 有外键约束）。
	var expID int64
	if err := d.QueryRow(`INSERT INTO explorations(goal) VALUES ('dedup integration test') RETURNING id`).Scan(&expID); err != nil {
		t.Fatalf("create exploration: %v", err)
	}
	var taskID int64
	if err := d.QueryRow(`INSERT INTO tasks(description, goal, exploration_id) VALUES('dedup-test','dedup-test',$1) RETURNING id`, expID).Scan(&taskID); err != nil {
		t.Fatalf("create task: %v", err)
	}
	t.Cleanup(func() {
		d.Exec(`DELETE FROM findings WHERE task_id=$1`, taskID)
		d.Exec(`DELETE FROM exploration_nodes WHERE exploration_id=$1`, expID)
		d.Exec(`DELETE FROM exploration_anchors WHERE exploration_id=$1`, expID)
		d.Exec(`DELETE FROM exploration_edges WHERE exploration_id=$1`, expID)
		d.Exec(`DELETE FROM activity WHERE exploration_id=$1`, expID)
		d.Exec(`DELETE FROM tasks WHERE id=$1`, taskID)
		d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	})

	in := RecordFindingInput{
		TaskID: taskID, ExplorationID: expID, IntentID: 0,
		VulnClass: "sqli", Name: "SQL 注入", Severity: "high",
		Summary:  "登录接口存在 SQL 注入",
		Evidence: "curl http://dedup-check.test/api/login?user=1 => 200",
		Worker:   "work#1", AssetIDs: []int64{},
	}

	var first *RecordedFinding
	if err := d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var e error
		first, e = RecordFindingTx(ctx, tx, in, nil)
		return e
	}); err != nil {
		t.Fatalf("first record: %v", err)
	}
	if first.DuplicateOf != 0 {
		t.Fatalf("首次上报不应是 duplicate（dupOf=%d）", first.DuplicateOf)
	}

	// 同类别、同端点，只有 query/协议不同 —— 应判为同一条
	in.Evidence = "GET https://dedup-check.test/api/login?user=2&x=1 => 200"
	var second *RecordedFinding
	if err := d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var e error
		second, e = RecordFindingTx(ctx, tx, in, nil)
		return e
	}); err != nil {
		t.Fatalf("second record: %v", err)
	}
	if second.DuplicateOf != first.FindingID {
		t.Fatalf("duplicate_of = %d, want %d", second.DuplicateOf, first.FindingID)
	}

	var status string
	if err := d.QueryRow(`SELECT status FROM findings WHERE id=$1`, second.FindingID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "duplicate" {
		t.Fatalf("status = %q, want duplicate", status)
	}

}
