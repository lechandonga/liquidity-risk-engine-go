package liquidity

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentWritesAndReads hammers the engine with parallel writers and
// readers. Synchronisation uses WaitGroups/channels only — no sleeps. Run
// with -race.
func TestConcurrentWritesAndReads(t *testing.T) {
	e := NewEngine()
	mustPublish(t, e, "BASE", 1, baseAssumptions())
	mustPublish(t, e, "STRESS", 1, &Assumptions{
		DemandRunoffRate: f64(0.4), TermRolloverRate: f64(0.1), WholesaleRollRate: f64(0.0),
		CreditLineDrawRate: f64(0.8), SecurityHaircut: f64(0.3), LoanPrepayRate: f64(0.2),
	})
	start := NewDate(2026, 1, 5)
	asOf := NewDate(2026, 1, 10)

	const writers = 8
	const batchesPerWriter = 25
	const readers = 8

	startGate := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, writers+readers)

	// Writers: each commits distinct batches; half of them also replay an old
	// batch to exercise idempotency under concurrency.
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-startGate
			for i := 0; i < batchesPerWriter; i++ {
				id := fmt.Sprintf("W%d-B%d", w, i)
				_, err := e.Ingest(Batch{BatchID: id, Events: []Event{
					bookEvent("E-"+id, termDeposit("P-"+id, Money(1000*(i+1)), start, Date{start.AddDate(0, 0, 30+i)})),
				}})
				if err != nil {
					errCh <- fmt.Errorf("writer %d: %w", w, err)
					return
				}
				if i%2 == 0 { // replay must be a safe no-op
					if _, err := e.Ingest(Batch{BatchID: id, Events: []Event{
						bookEvent("E-"+id, termDeposit("P-"+id, 1, start, Date{start.AddDate(0, 0, 30+i)})),
					}}); err != nil {
						errCh <- fmt.Errorf("writer %d replay: %w", w, err)
						return
					}
				}
			}
		}(w)
	}

	// Readers: snapshot + scenario runs must never error and always observe a
	// consistent (fully applied) state.
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			<-startGate
			for i := 0; i < 200; i++ {
				snap := e.Snapshot()
				scenario := "BASE"
				if i%2 == 1 {
					scenario = "STRESS"
				}
				m, err := e.RunScenario(snap, scenario, 1, asOf)
				if err != nil {
					errCh <- fmt.Errorf("reader %d: %w", r, err)
					return
				}
				if m.SnapshotSeq != snap.Seq {
					errCh <- fmt.Errorf("reader %d: metrics seq %d != snapshot seq %d", r, m.SnapshotSeq, snap.Seq)
					return
				}
			}
		}(r)
	}

	close(startGate)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	final := e.Snapshot()
	if n := len(final.Positions()); n != writers*batchesPerWriter {
		t.Fatalf("expected %d positions, got %d (replay double-counted or write lost)",
			writers*batchesPerWriter, n)
	}
	// Every snapshot a reader could have taken remains reproducible now.
	m, err := e.RunScenario(final, "BASE", 1, asOf)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("[并发一致性] %d 写者 x %d 批次 + %d 读者并发完成；最终头寸=%d，重放无重复计数，最终结论 %s",
		writers, batchesPerWriter, readers, len(final.Positions()), m)
}

// TestSnapshotIsolationUnderConcurrentCorrection proves a snapshot taken
// before a correction never observes it, even while corrections stream in.
func TestSnapshotIsolationUnderConcurrentCorrection(t *testing.T) {
	e := NewEngine()
	mustPublish(t, e, "BASE", 1, baseAssumptions())
	start := NewDate(2026, 1, 5)
	asOf := NewDate(2026, 1, 10)
	if _, err := e.Ingest(Batch{BatchID: "SEED", Events: []Event{
		bookEvent("E0", termDeposit("P0", 100_00, start, Date{start.AddDate(0, 0, 20)})),
	}}); err != nil {
		t.Fatal(err)
	}
	pinned := e.Snapshot()
	before, err := e.RunScenario(pinned, "BASE", 1, asOf)
	if err != nil {
		t.Fatal(err)
	}

	const corrections = 50
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < corrections; i++ {
			amt := Money(100_00 + i)
			p := termDeposit("P0", amt, start, Date{start.AddDate(0, 0, 20)})
			if _, err := e.Ingest(Batch{BatchID: fmt.Sprintf("C%d", i), Events: []Event{
				{ID: fmt.Sprintf("EC%d", i), Kind: EventCorrect, PositionID: "P0", Position: p},
			}}); err != nil {
				t.Errorf("correction %d: %v", i, err)
				return
			}
		}
	}()
	// Concurrently recompute against the pinned snapshot: result must be
	// bit-identical every time.
	for i := 0; i < 100; i++ {
		m, err := e.RunScenario(pinned, "BASE", 1, asOf)
		if err != nil {
			t.Fatal(err)
		}
		if m.Ladder.Flows != before.Ladder.Flows {
			t.Fatalf("pinned snapshot conclusion changed under concurrent corrections")
		}
	}
	<-done
	t.Logf("[快照隔离] 固定快照在 %d 笔并发更正下重算 100 次结论完全一致；已发布结论不被篡改", corrections)
}
