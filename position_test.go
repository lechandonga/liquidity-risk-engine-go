package liquidity

import (
	"testing"
	"time"
)

func TestPositionValidationRejectsDirtyData(t *testing.T) {
	start := NewDate(2026, 1, 5)
	mat := NewDate(2026, 7, 5)
	valid := termDeposit("P1", 1_000_00, start, mat)

	cases := []struct {
		name   string
		mutate func(*Position)
		code   RejectCode
	}{
		{"missing id", func(p *Position) { p.ID = "" }, CodeMissingField},
		{"missing currency", func(p *Position) { p.Currency = "" }, CodeMissingField},
		{"missing direction", func(p *Position) { p.Direction = "" }, CodeMissingField},
		{"zero amount", func(p *Position) { p.Amount = 0 }, CodeInvalidAmount},
		{"negative amount", func(p *Position) { p.Amount = -5 }, CodeInvalidAmount},
		{"maturity before start", func(p *Position) { p.MaturityDate = NewDate(2025, 1, 1) }, CodeInvalidTenor},
		{"maturity equals start", func(p *Position) { p.MaturityDate = start }, CodeInvalidTenor},
		{"missing maturity on term deposit", func(p *Position) { p.MaturityDate = Date{} }, CodeMissingField},
		{"maturity on demand deposit", func(p *Position) {
			p.Type = DemandDeposit
			p.Direction = Outflow
		}, CodeInvalidTenor},
		{"direction mismatch: deposit as inflow", func(p *Position) { p.Direction = Inflow }, CodeDirectionMismatch},
		{"direction mismatch: loan as outflow", func(p *Position) {
			p.Type = LoanRepayment
			p.Direction = Outflow
		}, CodeDirectionMismatch},
		{"unknown type", func(p *Position) { p.Type = "CRYPTO" }, CodeMissingField},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.mutate(&p)
			err := p.validate()
			if err == nil {
				t.Fatalf("expected rejection %s, got nil", tc.code)
			}
			if !IsReject(err, tc.code) {
				t.Fatalf("expected code %s, got %v", tc.code, err)
			}
			t.Logf("[头寸校验] %s -> 拒绝原因 %s (%v)", tc.name, tc.code, err)
		})
	}
}

func TestDuplicatePositionRegistrationRejected(t *testing.T) {
	e := NewEngine()
	start := NewDate(2026, 1, 5)
	mat := start.AddDate(0, 6, 0)
	matD := Date{mat}
	b1, err := e.Ingest(Batch{BatchID: "B1", Events: []Event{
		bookEvent("E1", termDeposit("P1", 100_00, start, matD)),
	}})
	if err != nil {
		t.Fatalf("first booking: %v", err)
	}
	_, err = e.Ingest(Batch{BatchID: "B2", Events: []Event{
		bookEvent("E2", termDeposit("P1", 200_00, start, matD)),
	}})
	if !IsReject(err, CodeDuplicatePosition) {
		t.Fatalf("expected DUPLICATE_POSITION, got %v", err)
	}
	// The rejected batch must not have modified state.
	if got := len(e.Snapshot().Positions()); got != 1 {
		t.Fatalf("rejected batch mutated state: %d positions", got)
	}
	if e.Snapshot().Positions()["P1"].Amount != 100_00 {
		t.Fatalf("rejected batch overwrote amount")
	}
	t.Logf("[重复登记] 批次 B1 登记成功(seq=%d)，B2 重复登记 P1 被拒绝且状态未被污染", b1.Seq)
}

func TestBucketingIndependentOfRegistrationOrder(t *testing.T) {
	start := NewDate(2026, 1, 5)
	asOf := NewDate(2026, 1, 10)
	mk := func(order []string) *Ladder {
		e := NewEngine()
		mustPublish(t, e, "BASE", 1, baseAssumptions())
		var evs []Event
		positions := map[string]Position{
			"A": termDeposit("A", 700_00, start, Date{start.AddDate(0, 0, 20)}),
			"B": demandDeposit("B", 500_00, start),
			"C": security("C", 900_00, start, Date{start.AddDate(0, 3, 0)}),
			"D": {ID: "D", Type: LoanRepayment, Direction: Inflow, Currency: "CNY",
				Counterparty: "cp-D", Amount: 300_00, StartDate: start, MaturityDate: Date{start.AddDate(0, 1, 0)}},
		}
		for i, id := range order {
			evs = append(evs, bookEvent("E-"+id, positions[id]))
			_ = i
		}
		if _, err := e.Ingest(Batch{BatchID: "B-" + order[0] + order[1], Events: evs}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
		m, err := e.RunScenario(e.Snapshot(), "BASE", 1, asOf)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		return m.Ladder
	}
	l1 := mk([]string{"A", "B", "C", "D"})
	l2 := mk([]string{"D", "C", "B", "A"})
	if l1.Flows != l2.Flows {
		t.Fatalf("bucket ladder depends on registration order:\n%v\n%v", l1.Flows, l2.Flows)
	}
	t.Logf("[分桶确定性] 两种相反登记顺序得到完全一致的现金流阶梯: %v", l1.Flows)
}

func TestNetCashflowQueryAtAnyDate(t *testing.T) {
	e := NewEngine()
	mustPublish(t, e, "BASE", 1, baseAssumptions())
	start := NewDate(2026, 1, 5)
	asOf := NewDate(2026, 1, 10)
	mat := Date{start.AddDate(0, 0, 20)} // 15 days after asOf -> D15_30 bucket
	if _, err := e.Ingest(Batch{BatchID: "B1", Events: []Event{
		bookEvent("E1", termDeposit("P1", 1_000_00, start, mat)),
	}}); err != nil {
		t.Fatal(err)
	}
	snap := e.Snapshot()
	// rollover 50% => outflow 500_00 in the D15_30 bucket.
	got, err := e.NetCashflowAt(snap, "BASE", 1, asOf, Date{asOf.AddDate(0, 0, 16)})
	if err != nil {
		t.Fatal(err)
	}
	if got != -500_00 {
		t.Fatalf("expected -50000 in D15_30 bucket, got %d", got)
	}
	if _, err := e.NetCashflowAt(snap, "BASE", 1, asOf, NewDate(2026, 1, 1)); err == nil {
		t.Fatalf("querying before as-of must fail")
	}
	t.Logf("[时点查询] asOf=%s 查询 %s 净现金流=%d（50%% 续作假设）", asOf, mat, got)
}

func TestBatchAtomicityNoPartialApplication(t *testing.T) {
	e := NewEngine()
	start := NewDate(2026, 1, 5)
	good := bookEvent("E1", termDeposit("P1", 100_00, start, Date{start.AddDate(0, 6, 0)}))
	bad := bookEvent("E2", termDeposit("P2", -1, start, Date{start.AddDate(0, 6, 0)}))
	_, err := e.Ingest(Batch{BatchID: "B1", Events: []Event{good, bad}})
	if err == nil {
		t.Fatal("expected batch rejection")
	}
	if n := len(e.Snapshot().Positions()); n != 0 {
		t.Fatalf("half-applied batch: %d positions leaked", n)
	}
	t.Logf("[批次原子性] 含脏数据的批次整体拒绝，无任何头寸残留 (err=%v)", err)
}

var _ = time.Now // keep time import if unused in future edits
