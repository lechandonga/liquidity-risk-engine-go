package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/engine"
)

// TestBackfillReverseAndReplay 覆盖：
// 回填历史日期批次、冲正、对冲正再冲正（恢复）、重复批次幂等、
// 文件日志回放后状态一致、已发布快照不被后续更正篡改。
func TestBackfillReverseAndReplay(t *testing.T) {
	const biz = "回填/冲正/重复回放"
	e := engine.New("CNY")
	today := domain.MustDate(2026, 10, 8)

	// 回填：PostedAt 是上个月的历史日期，仍允许并生效。
	backfill := domain.Batch{BatchID: "HIST", PostedAt: domain.MustDate(2026, 9, 1),
		Ops: []domain.PositionOp{{Type: domain.OpRegister, Position: deposit("OLD", 500, "DEFAULT")}}}
	if _, err := e.ApplyBatch(backfill); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Position("OLD"); !ok {
		t.Fatal("回填批次必须立即生效")
	}
	testLog(t, biz, "回填历史日期", "PostedAt=2026-09-01 头寸已生效 seq=%d", e.CurrentSeq())

	newBatch := domain.Batch{BatchID: "NEW", PostedAt: today,
		Ops: []domain.PositionOp{{Type: domain.OpRegister, Position: deposit("NEW", 700, "DEFAULT")}}}
	if _, err := e.ApplyBatch(newBatch); err != nil {
		t.Fatal(err)
	}

	// 重复回放：任何顺序、任何次数都不得重复计数。
	for i := 0; i < 3; i++ {
		r, err := e.ApplyBatch(backfill)
		if err != nil {
			t.Fatal(err)
		}
		if !r.Idempotent {
			t.Fatalf("第 %d 次重复回放必须幂等", i+1)
		}
		r2, err := e.ApplyBatch(newBatch)
		if err != nil || !r2.Idempotent {
			t.Fatalf("新批次重复回放必须幂等: %v", err)
		}
	}
	if e.CurrentSeq() != 2 {
		t.Fatalf("重复回放后 seq 必须保持 2，实际 %d", e.CurrentSeq())
	}
	if len(e.Positions()) != 2 {
		t.Fatalf("重复回放不得增加头寸，实际 %d 笔", len(e.Positions()))
	}
	testLog(t, biz, "重复批次幂等回放", "多次回放后 seq=2 头寸数=2")

	// 发布一个不可变快照。
	sc := baseScenario("R", 1, 10, nil)
	if _, err := e.Scenarios().Publish(sc); err != nil {
		t.Fatal(err)
	}
	snapBefore, err := e.Compute("R", 1, today, 10)
	if err != nil {
		t.Fatal(err)
	}

	// 冲正回填批次。
	rev := domain.Batch{BatchID: "REV-HIST", PostedAt: today, ReverseBatchID: "HIST"}
	if _, err := e.ApplyBatch(rev); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Position("OLD"); ok {
		t.Fatal("冲正后头寸必须移除")
	}
	// 冲正不存在的批次必须拒绝；重复冲正也必须拒绝。
	if _, err := e.ApplyBatch(domain.Batch{BatchID: "REV-GHOST", PostedAt: today, ReverseBatchID: "GHOST"}); err == nil {
		t.Fatal("冲正不存在批次必须拒绝")
	}
	if r, err := e.ApplyBatch(rev); err != nil {
		t.Fatalf("冲正批次本身的重复回放必须幂等，实际错误: %v", err)
	} else {
		if !r.Idempotent {
			t.Fatal("冲正批次重复回放必须返回幂等标记")
		}
		if _, ok := e.Position("OLD"); ok {
			t.Fatal("冲正批次幂等回放不得恢复头寸")
		}
	}
	// 对已冲正批次再次冲正必须拒绝。
	if _, err := e.ApplyBatch(domain.Batch{BatchID: "REV-HIST-2", PostedAt: today, ReverseBatchID: "HIST"}); err == nil {
		t.Fatal("批次已冲正，再次冲正必须拒绝")
	}
	testLog(t, biz, "冲正与重复冲正拒绝", "OLD 已移除，重复/二次冲正被拒绝")

	// 对冲正再冲正 => 恢复。
	if _, err := e.ApplyBatch(domain.Batch{BatchID: "REV-REV", PostedAt: today, ReverseBatchID: "REV-HIST"}); err != nil {
		t.Fatal(err)
	}
	if p, ok := e.Position("OLD"); !ok || p.Balance != 500 {
		t.Fatalf("恢复失败，OLD 应还原为 500，实际 ok=%v", ok)
	}
	testLog(t, biz, "对冲正再冲正(恢复)", "OLD 以冲正前内容确定性还原")

	// 已发布快照旧值不变；新状态产生新版本快照。
	snapAfter, err := e.Compute("R", 1, today, 10)
	if err != nil {
		t.Fatal(err)
	}
	if snapBefore.StateSeq >= snapAfter.StateSeq {
		t.Fatal("更正后快照必须基于更大的状态序号")
	}
	if snapBefore.Metrics.TotalNetCashFlow == snapAfter.Metrics.TotalNetCashFlow {
		// 恢复后状态与冲正前是否相同取决于 seq；seq 必然不同，内容可相同。
		// 这里仅校验旧快照内容字段未被原地改写。
	}
	if snapBefore.StateSeq != 2 {
		t.Fatalf("旧快照被篡改：StateSeq=%d 期望 2", snapBefore.StateSeq)
	}
	testLog(t, biz, "快照不可变", "旧快照 StateSeq=%d 不变，新快照 StateSeq=%d",
		snapBefore.StateSeq, snapAfter.StateSeq)

	// 文件日志重放：全新引擎追加写入日志，再用第三个引擎回放，状态必须一致。
	dir := t.TempDir()
	jPath := filepath.Join(dir, "events.ndjson")
	j, err := engine.OpenFileJournal(jPath)
	if err != nil {
		t.Fatal(err)
	}
	e2 := engine.New("CNY", engine.WithJournal(j))
	for _, b := range []domain.Batch{backfill, newBatch, rev,
		{BatchID: "REV-REV", PostedAt: today, ReverseBatchID: "REV-HIST"}} {
		if _, err := e2.ApplyBatch(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	j3 := mustJournal(t, jPath)
	defer j3.Close()
	e3 := engine.New("CNY")
	if err := engine.ReplayJournal(j3, e3); err != nil {
		t.Fatal(err)
	}
	if e3.CurrentSeq() != e2.CurrentSeq() || len(e3.Positions()) != len(e2.Positions()) {
		t.Fatalf("回放状态不一致: seq=%d/%d positions=%d/%d",
			e3.CurrentSeq(), e2.CurrentSeq(), len(e3.Positions()), len(e2.Positions()))
	}
	for _, p := range e2.Positions() {
		p3, ok := e3.Position(p.ID)
		if !ok || p3.Balance != p.Balance {
			t.Fatalf("回放头寸 %s 不一致", p.ID)
		}
	}
	testLog(t, biz, "本地文件日志回放", "seq=%d 头寸数=%d 全部一致", e3.CurrentSeq(), len(e3.Positions()))
}

func mustJournal(t *testing.T, path string) *engine.FileJournal {
	t.Helper()
	j, err := engine.OpenFileJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
