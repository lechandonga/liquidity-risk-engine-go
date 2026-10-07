package liquidity

import "testing"

func TestScenarioValidationRejectsInvalidDefinitions(t *testing.T) {
	reg := NewScenarioRegistry()
	cases := []struct {
		name string
		sc   *Scenario
		code RejectCode
	}{
		{"missing id", &Scenario{Version: 1, Full: baseAssumptions()}, CodeScenarioInvalid},
		{"version zero", &Scenario{ID: "S", Version: 0, Full: baseAssumptions()}, CodeScenarioInvalid},
		{"root without assumptions", &Scenario{ID: "S", Version: 1}, CodeScenarioInvalid},
		{"missing parameter", &Scenario{ID: "S", Version: 1, Full: &Assumptions{
			DemandRunoffRate: f64(0.1), TermRolloverRate: f64(0.5), WholesaleRollRate: f64(0.2),
			CreditLineDrawRate: f64(0.3), SecurityHaircut: f64(0.15)}}, CodeScenarioInvalid},
		{"rate above 1", &Scenario{ID: "S", Version: 1, Full: &Assumptions{
			DemandRunoffRate: f64(1.5), TermRolloverRate: f64(0.5), WholesaleRollRate: f64(0.2),
			CreditLineDrawRate: f64(0.3), SecurityHaircut: f64(0.15), LoanPrepayRate: f64(0.05)}}, CodeScenarioInvalid},
		{"negative rate", &Scenario{ID: "S", Version: 1, Full: &Assumptions{
			DemandRunoffRate: f64(-0.1), TermRolloverRate: f64(0.5), WholesaleRollRate: f64(0.2),
			CreditLineDrawRate: f64(0.3), SecurityHaircut: f64(0.15), LoanPrepayRate: f64(0.05)}}, CodeScenarioInvalid},
		{"overlay without base version", &Scenario{ID: "S", Version: 2, Base: "B",
			Overrides: &Assumptions{DemandRunoffRate: f64(0.2)}}, CodeScenarioInvalid},
		{"overlay without overrides", &Scenario{ID: "S", Version: 2, Base: "B", BaseVersion: 1}, CodeScenarioInvalid},
		{"overlay with undefined base", &Scenario{ID: "S", Version: 2, Base: "NOPE", BaseVersion: 1,
			Overrides: &Assumptions{DemandRunoffRate: f64(0.2)}}, CodeUnknownScenario},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := reg.Publish(tc.sc)
			if !IsReject(err, tc.code) {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
			t.Logf("[情景校验] %s -> 拒绝 %s", tc.name, tc.code)
		})
	}
}

func TestScenarioVersionSwitchAndReproducibility(t *testing.T) {
	e := NewEngine()
	mustPublish(t, e, "STRESS", 1, baseAssumptions())

	// v2: harsher stress as an overlay on pinned v1.
	harsher := &Assumptions{DemandRunoffRate: f64(0.40)}
	if err := e.Scenario.Publish(&Scenario{ID: "STRESS", Version: 2, Base: "STRESS", BaseVersion: 1, Overrides: harsher}); err != nil {
		t.Fatal(err)
	}
	// Republishing an existing version must be rejected (immutability).
	if err := e.Scenario.Publish(&Scenario{ID: "STRESS", Version: 1, Full: baseAssumptions()}); !IsReject(err, CodeScenarioInvalid) {
		t.Fatalf("republish of v1 must be rejected, got %v", err)
	}

	start := NewDate(2026, 1, 5)
	asOf := NewDate(2026, 1, 10)
	if _, err := e.Ingest(Batch{BatchID: "B1", Events: []Event{
		bookEvent("E1", demandDeposit("D1", 1_000_00, start)),
	}}); err != nil {
		t.Fatal(err)
	}
	snap := e.Snapshot()

	m1, err := e.RunScenario(snap, "STRESS", 1, asOf)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := e.RunScenario(snap, "STRESS", 2, asOf)
	if err != nil {
		t.Fatal(err)
	}
	// v1 runoff 10% -> overnight outflow 100_00; v2 runoff 40% -> 400_00.
	if m1.Ladder.Flows[0] != -100_00 || m2.Ladder.Flows[0] != -400_00 {
		t.Fatalf("version switch wrong: v1=%d v2=%d", m1.Ladder.Flows[0], m2.Ladder.Flows[0])
	}
	// Mutating the caller's override struct afterwards must not leak into the
	// published version.
	*harsher.DemandRunoffRate = 0.99
	m2again, err := e.RunScenario(snap, "STRESS", 2, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if m2again.Ladder.Flows[0] != -400_00 {
		t.Fatalf("published v2 was mutated by caller: %d", m2again.Ladder.Flows[0])
	}
	// Re-running v1 after v2 exists reproduces the original conclusion.
	m1again, err := e.RunScenario(snap, "STRESS", 1, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if m1again.Ladder.Flows != m1.Ladder.Flows {
		t.Fatalf("v1 conclusion changed after v2 published")
	}
	t.Logf("[情景版本] v1 隔夜流出=%d, v2 隔夜流出=%d；历史版本结论可原样复现，新版本不污染旧结论",
		m1.Ladder.Flows[0], m2.Ladder.Flows[0])
}

func TestOverlayChainDeterministic(t *testing.T) {
	reg := NewScenarioRegistry()
	if err := reg.Publish(&Scenario{ID: "BASE", Version: 1, Full: baseAssumptions()}); err != nil {
		t.Fatal(err)
	}
	// v2 overrides runoff; v3 overlays v2 and overrides haircut only.
	if err := reg.Publish(&Scenario{ID: "S", Version: 2, Base: "BASE", BaseVersion: 1,
		Overrides: &Assumptions{DemandRunoffRate: f64(0.25)}}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Publish(&Scenario{ID: "S", Version: 3, Base: "S", BaseVersion: 2,
		Overrides: &Assumptions{SecurityHaircut: f64(0.50)}}); err != nil {
		t.Fatal(err)
	}
	a, err := reg.Resolve("S", 3)
	if err != nil {
		t.Fatal(err)
	}
	if *a.DemandRunoffRate != 0.25 || *a.SecurityHaircut != 0.50 || *a.TermRolloverRate != 0.50 {
		t.Fatalf("overlay chain wrong: %+v", a)
	}
	t.Logf("[情景叠加] v3 = BASE(v1) + v2(runoff=0.25) + v3(haircut=0.50)，字段级覆盖确定: runoff=%.2f haircut=%.2f rollover=%.2f",
		*a.DemandRunoffRate, *a.SecurityHaircut, *a.TermRolloverRate)
}

func TestMetricsUnderStress(t *testing.T) {
	e := NewEngine()
	mustPublish(t, e, "BASE", 1, baseAssumptions())
	start := NewDate(2026, 1, 5)
	asOf := NewDate(2026, 1, 10)
	if _, err := e.Ingest(Batch{BatchID: "B1", Events: []Event{
		bookEvent("E1", demandDeposit("D1", 1_000_00, start)),                            // runoff 100_00 overnight
		bookEvent("E2", security("S1", 500_00, start, Date{start.AddDate(2, 0, 0)})),     // HQLA 425_00
		bookEvent("E3", termDeposit("T1", 200_00, start, Date{start.AddDate(0, 0, 10)})), // 50% roll -> 100_00 out @D8_14
	}}); err != nil {
		t.Fatal(err)
	}
	m, err := e.RunScenario(e.Snapshot(), "BASE", 1, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if m.HQLA != 425_00 {
		t.Fatalf("HQLA=%d want 42500", m.HQLA)
	}
	if m.NetOutflow30D != 200_00 {
		t.Fatalf("NetOutflow30D=%d want 20000", m.NetOutflow30D)
	}
	if m.LCR < 2.12 || m.LCR > 2.13 {
		t.Fatalf("LCR=%f want ~2.125", m.LCR)
	}
	if m.SurvivalDays != 399 {
		t.Fatalf("SurvivalDays=%d want 399 (demand core outflow beyond 1y drains the buffer)", m.SurvivalDays)
	}
	t.Logf("[压力指标] %s", m)
}
