// Command demo runs a small end-to-end liquidity stress example locally.
package main

import (
	"fmt"
	"log"

	liquidity "github.com/lechandonga/liquidity-risk-engine-go"
)

func f64(v float64) *float64 { return &v }

func main() {
	e := liquidity.NewEngine()
	start := liquidity.NewDate(2026, 1, 5)
	asOf := liquidity.NewDate(2026, 1, 10)

	if err := e.Scenario.Publish(&liquidity.Scenario{ID: "BASE", Version: 1, Full: &liquidity.Assumptions{
		DemandRunoffRate: f64(0.10), TermRolloverRate: f64(0.50), WholesaleRollRate: f64(0.20),
		CreditLineDrawRate: f64(0.30), SecurityHaircut: f64(0.15), LoanPrepayRate: f64(0.05),
	}}); err != nil {
		log.Fatal(err)
	}
	if err := e.Scenario.Publish(&liquidity.Scenario{ID: "STRESS", Version: 1, Base: "BASE", BaseVersion: 1,
		Overrides: &liquidity.Assumptions{DemandRunoffRate: f64(0.40), SecurityHaircut: f64(0.40)}}); err != nil {
		log.Fatal(err)
	}

	d := func(days int) liquidity.Date { return liquidity.Date{Time: start.AddDate(0, 0, days)} }
	if _, err := e.Ingest(liquidity.Batch{BatchID: "B1", Events: []liquidity.Event{
		{ID: "E1", Kind: liquidity.EventBook, BusinessDate: start, Position: liquidity.Position{
			ID: "DEP1", Type: liquidity.DemandDeposit, Direction: liquidity.Outflow, Currency: "CNY",
			Counterparty: "retail", Amount: 1_000_000_00, StartDate: start}},
		{ID: "E2", Kind: liquidity.EventBook, BusinessDate: start, Position: liquidity.Position{
			ID: "SEC1", Type: liquidity.Security, Direction: liquidity.Inflow, Currency: "CNY",
			Counterparty: "treasury", Amount: 600_000_00, StartDate: start, MaturityDate: d(400)}},
		{ID: "E3", Kind: liquidity.EventBook, BusinessDate: start, Position: liquidity.Position{
			ID: "LOAN1", Type: liquidity.LoanRepayment, Direction: liquidity.Inflow, Currency: "CNY",
			Counterparty: "corp", Amount: 300_000_00, StartDate: start, MaturityDate: d(25)}},
	}}); err != nil {
		log.Fatal(err)
	}

	snap := e.Snapshot()
	for _, sc := range []string{"BASE", "STRESS"} {
		m, err := e.RunScenario(snap, sc, 1, asOf)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(m)
		fmt.Printf("  buckets %v %v\n", liquidity.BucketNames(), m.Ladder.Flows)
	}
}
