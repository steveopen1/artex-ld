// liveness.go：启动卫生 —— 把崩溃后残留的"假存活"状态诚实化。
//
// 背景：进程被 kill / 机器重启时，内存里的运行态全部丢失，但数据库里的状态
// 还停在"在跑"。不清理的后果是任务在 UI 上一直显示运行中、复测一直显示"进行中"，
// 而实际上没有任何协程在推进它们 —— 用户看到的是僵尸，且无法从界面判断
// 到底是"慢"还是"死了"。
//
// 已有机制与缺口：
//   - exploration_nodes 的 running 意图：restoreTaskRuntimes 里已经重置为 open；
//   - finding_retests 的 pending/running：manager 启动时调 RecoverFindingRetests；
//   - **任务本身**：重启后若其状态仍是 running 且没有引擎实例接管，就没人处理它。
//
// 本文件补最后一条：启动后异步核对每个"非终态"任务，确认引擎里确实有它的
// 运行实例；没有的，如实标记并记审计，而不是让它在列表里挂着一个假的运行中。
package server

import (
	"fmt"
	"log"
	"time"

	"artex/db"
)

// reconcileStaleRuntimes 在启动后异步核对任务运行态。
// 并发执行（每个任务一个 goroutine），失败只记日志不中断 —— 启动卫生
// 属于尽力而为：它坏了不该拖垮整个启动流程。
func (s *Server) reconcileStaleRuntimes() {
	// 给 restoreTaskRuntimes 之后的 Run() 一点时间把实例注册进引擎，
	// 否则会把"刚要启动"误判成"没有人管"。
	select {
	case <-s.ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}

	tasks := s.m.List()
	sem := make(chan struct{}, 8) // 并发上限：避免大量任务同时打库
	done := make(chan struct{}, len(tasks))
	for _, t := range tasks {
		t := t
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; done <- struct{}{} }()
			s.reconcileOneTask(t)
		}()
	}
	for range tasks {
		<-done
	}
}

func (s *Server) reconcileOneTask(t *Task) {
	if t == nil {
		return
	}
	lc := t.lifecycleSnapshot()
	if isTerminalStatus(lc.Status) {
		return // 已终态：本就该没有运行实例
	}
	if lc.Queued {
		return // 排队中：没有实例是正常的
	}
	if lc.Paused {
		return // 暂停中：没有实例是正常的
	}
	if !s.engine.Started(t.ID) {
		// 诚实标注：它不是 running，只是没人管。
		// 用 paused 而不是 failed —— failed 会污染任务统计与报告口径，
		// 而"没有运行实例"本身的正确处置是等人/等调度把它拉起来。
		if _, err := s.m.SetTaskStatusGuarded(t.ID, "paused"); err != nil {
			log.Printf("[liveness] task %s 状态修正失败: %v", t.ID, err)
			return
		}
		log.Printf("[liveness] task %s 重启后无运行实例，已标记 paused（原状态 %s）", t.ID, lc.Status)
		if t.Store != nil {
			_, _ = t.Store.AppendActivity(db.Activity{
				Worker: "system", Kind: "liveness_reconcile", IsError: false,
				Summary: fmt.Sprintf("启动核对：本任务重启后没有运行实例，已把状态从“%s”修正为“暂停”", lc.Status),
				Detail:  fmt.Sprintf(`{"previous_status":%q}`, lc.Status),
			})
		}
	}
}
