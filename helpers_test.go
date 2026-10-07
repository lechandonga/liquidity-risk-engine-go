package liquidity

import "testing"

func f64(v float64) *float64 { return &v }

// baseAssumptions returns a complete, valid assumption set.
func baseAssumptions() *Assumptions {
	return &Assumptions{
		DemandRunoffRate:   f64(0.10),
		TermRolloverRate:   f64(0.50),
		WholesaleRollRate:  f64(0.20),
		CreditLineDrawRate: f64(0.30),
		SecurityHaircut:    f64(0.15),
		LoanPrepayRate:     f64(0.05),
	}
}

func mustPublish(t *testing.T, e *Engine, id string, version int, full *Assumptions) {
	t.Helper()
	if err := e.Scenario.Publish(&Scenario{ID: id, Version: version, Full: full}); err != nil {
		t.Fatalf("publish scenario %s v%d: %v", id, version, err)
	}
}

func bookEvent(id string, p Position) Event {
	return Event{ID: id, Kind: EventBook, Position: p, BusinessDate: p.StartDate}
}

func termDeposit(id string, amount Money, start, mat Date) Position {
	return Position{ID: id, Type: TermDeposit, Direction: Outflow, Currency: "CNY",
		Counterparty: "cp-" + id, Amount: amount, StartDate: start, MaturityDate: mat}
}

func demandDeposit(id string, amount Money, start Date) Position {
	return Position{ID: id, Type: DemandDeposit, Direction: Outflow, Currency: "CNY",
		Counterparty: "cp-" + id, Amount: amount, StartDate: start}
}

func security(id string, amount Money, start, mat Date) Position {
	return Position{ID: id, Type: Security, Direction: Inflow, Currency: "CNY",
		Counterparty: "cp-" + id, Amount: amount, StartDate: start, MaturityDate: mat}
}
