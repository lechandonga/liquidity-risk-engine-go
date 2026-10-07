package liquidity

import "testing"

func TestBackfillCorrectionAndReversalProduceNewVersions(t *testing.T) {
	e := NewEngine()
	mustPublish(t, e, "BASE", 1, baseAssumptions())
	start := NewDate(2026, 1, 5)
	asOf := NewDate(2026, 1, 10)

	// Initial booking.
	r1, err := e.Ingest(Batch{BatchID: "B1", Events: []Event{
		bookEvent("E1", termDeposit("P1", 100_00, start, Date{start.AddDate(0, 0, 20)})),
	}})
	if err != nil {
		t.Fatal(err)
	}
	snap1 := e.Snapshot()
	m1, err := e.RunScenario(snap1, "BASE", 1, asOf)
	if err != nil {
		t.Fatal(err)
	}

	// Backfilled correction: business date in the past, but it must land in a
	// NEW state version, never rewrite snap1.
	corrected := termDeposit("P1", 999_00, start, Date{start.AddDate(0, 0, 20)})
	r2, err := e.Ingest(Batch{BatchID: "B2", Events: []Event{
		{ID: "E2", Kind: EventCorrect, PositionID: "P1", Position: corrected,
			BusinessDate: NewDate(2026, 1, 6)}, // backfill
	}})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Seq <= r1.Seq {
		t.Fatalf("correction did not advance sequence: %d -> %d", r1.Seq, r2.Seq)
	}
	snap2 := e.Snapshot()
	m2, err := e.RunScenario(snap2, "BASE", 1, asOf)
	if err != nil {
		t.Fatal(err)
	}
	// Old snapshot conclusion is untouched; new snapshot reflects correction.
	if m1.Ladder.Flows == m2.Ladder.Flows {
		t.Fatalf("correction had no effect on new snapshot")
	}
	m1replay, err := e.RunScenario(snap1, "BASE", 1, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if m1replay.Ladder.Flows != m1.Ladder.Flows {
		t.Fatalf("published snapshot conclusion was rewritten by later correction")
	}

	// Reversal removes the position in a further new version.
	r3, err := e.Ingest(Batch{BatchID: "B3", Events: []Event{
		{ID: "E3", Kind: EventReverse, PositionID: "P1", BusinessDate: NewDate(2026, 1, 7)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(e.Snapshot().Positions()); n != 0 {
		t.Fatalf("reversal failed: %d positions remain", n)
	}
	t.Logf("[回填与冲正] seq %d(登记) -> %d(回填更正) -> %d(冲正)；旧快照结论保持不变，更正只产生新版本",
		r1.Seq, r2.Seq, r3.Seq)
}

func TestReplaySameBatchIsIdempotent(t *testing.T) {
	e := NewEngine()
	start := NewDate(2026, 1, 5)
	b := Batch{BatchID: "B1", Events: []Event{
		bookEvent("E1", termDeposit("P1", 100_00, start, Date{start.AddDate(0, 6, 0)})),
		bookEvent("E2", termDeposit("P2", 200_00, start, Date{start.AddDate(0, 6, 0)})),
	}}
	r1, err := e.Ingest(b)
	if err != nil {
		t.Fatal(err)
	}
	// Replay the exact same batch twice.
	for i := 0; i < 2; i++ {
		r, err := e.Ingest(b)
		if err != nil {
			t.Fatal(err)
		}
		if !r.Duplicate {
			t.Fatalf("replay %d not detected as duplicate", i)
		}
		if r.Seq != r1.Seq {
			t.Fatalf("replay advanced sequence: %d -> %d", r1.Seq, r.Seq)
		}
	}
	if n := len(e.Snapshot().Positions()); n != 2 {
		t.Fatalf("replay double-counted: %d positions", n)
	}
	// Same event id inside a different batch must also be rejected.
	_, err = e.Ingest(Batch{BatchID: "B2", Events: []Event{
		bookEvent("E1", termDeposit("P9", 1, start, Date{start.AddDate(0, 6, 0)})),
	}})
	if !IsReject(err, CodeDuplicatePosition) {
		t.Fatalf("reused event id must be rejected, got %v", err)
	}
	t.Logf("[幂等重放] 批次 B1 重放 2 次均被识别为重复(seq 不变)，头寸不重复计数；跨批次复用事件 ID 被拒绝")
}

func TestCorrectionOfUnknownPositionRejected(t *testing.T) {
	e := NewEngine()
	start := NewDate(2026, 1, 5)
	_, err := e.Ingest(Batch{BatchID: "B1", Events: []Event{
		{ID: "E1", Kind: EventCorrect, PositionID: "GHOST",
			Position: termDeposit("GHOST", 1, start, Date{start.AddDate(0, 6, 0)})},
	}})
	if !IsReject(err, CodeUnknownPosition) {
		t.Fatalf("expected UNKNOWN_POSITION, got %v", err)
	}
	_, err = e.Ingest(Batch{BatchID: "B2", Events: []Event{
		{ID: "E2", Kind: EventReverse, PositionID: "GHOST"},
	}})
	if !IsReject(err, CodeUnknownPosition) {
		t.Fatalf("expected UNKNOWN_POSITION, got %v", err)
	}
	t.Logf("[更正校验] 对不存在头寸的更正/冲正均以 UNKNOWN_POSITION 拒绝")
}
