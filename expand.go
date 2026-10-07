package liquidity

// Assumptions is the behavioural parameter set applied on top of contractual
// cashflows. Rates are fractions in [0,1]. A nil pointer means "parameter not
// provided" so scenario validation can reject incomplete definitions instead
// of silently assuming zero.
type Assumptions struct {
	DemandRunoffRate   *float64 // daily-runoff share of demand deposits in 30d
	TermRolloverRate   *float64 // share of maturing term funding rolled over
	WholesaleRollRate  *float64 // share of maturing wholesale funding rolled over
	CreditLineDrawRate *float64 // share of undrawn lines drawn within 30d
	SecurityHaircut    *float64 // haircut applied to securities for HQLA
	LoanPrepayRate     *float64 // share of loan principal prepaid within 30d
}

// field pairs: name -> (getter, setter) used for validation and overlay.
func assumptionFields() []struct {
	name string
	get  func(*Assumptions) *float64
	set  func(*Assumptions, *float64)
} {
	return []struct {
		name string
		get  func(*Assumptions) *float64
		set  func(*Assumptions, *float64)
	}{
		{"demand_runoff_rate", func(a *Assumptions) *float64 { return a.DemandRunoffRate }, func(a *Assumptions, v *float64) { a.DemandRunoffRate = v }},
		{"term_rollover_rate", func(a *Assumptions) *float64 { return a.TermRolloverRate }, func(a *Assumptions, v *float64) { a.TermRolloverRate = v }},
		{"wholesale_rollover_rate", func(a *Assumptions) *float64 { return a.WholesaleRollRate }, func(a *Assumptions, v *float64) { a.WholesaleRollRate = v }},
		{"credit_line_draw_rate", func(a *Assumptions) *float64 { return a.CreditLineDrawRate }, func(a *Assumptions, v *float64) { a.CreditLineDrawRate = v }},
		{"security_haircut", func(a *Assumptions) *float64 { return a.SecurityHaircut }, func(a *Assumptions, v *float64) { a.SecurityHaircut = v }},
		{"loan_prepay_rate", func(a *Assumptions) *float64 { return a.LoanPrepayRate }, func(a *Assumptions, v *float64) { a.LoanPrepayRate = v }},
	}
}

// clone deep-copies the assumption set (all pointer fields).
func (a *Assumptions) clone() *Assumptions {
	out := &Assumptions{}
	for _, f := range assumptionFields() {
		if v := f.get(a); v != nil {
			c := *v
			f.set(out, &c)
		}
	}
	return out
}

// expandPosition expands one position into dated cashflows under the given
// assumptions, relative to the as-of date. The expansion is a pure function
// of (position, assumptions, asOf) — fully deterministic.
func expandPosition(p *Position, a *Assumptions, asOf Date) []Cashflow {
	signed := signedAmount(p)
	var flows []Cashflow
	add := func(d Date, amt Money, label string) {
		if amt != 0 {
			flows = append(flows, Cashflow{PositionID: p.ID, Date: d, Amount: amt, Label: label})
		}
	}
	switch p.Type {
	case DemandDeposit:
		// Behavioural: runoff share leaves within the 30d window (bucketed
		// overnight), the stable core is assumed to stay beyond 1y.
		runoff := Money(float64(p.Amount) * *a.DemandRunoffRate)
		core := p.Amount - runoff
		add(asOf, -runoff, "demand_runoff")
		add(Date{asOf.AddDate(1, 0, 1)}, -core, "demand_core")
	case TermDeposit, WholesaleFunding:
		roll := *a.TermRolloverRate
		if p.Type == WholesaleFunding {
			roll = *a.WholesaleRollRate
		}
		// Only the non-rolled share of a maturity inside the horizon leaves.
		maturing := Money(float64(p.Amount) * (1 - roll))
		add(p.MaturityDate, -maturing, "maturity_outflow")
	case UndrawnCreditLine:
		// Stress drawdown assumed within the 30d window.
		drawn := Money(float64(p.Amount) * *a.CreditLineDrawRate)
		add(asOf, -drawn, "line_drawdown")
	case Security:
		// Securities are not contractual flows; they enter metrics as HQLA.
		// If they mature inside the horizon the redemption is an inflow.
		if !p.MaturityDate.IsZero() {
			add(p.MaturityDate, signed, "redemption")
		}
	case LoanRepayment:
		// Contractual repayment at maturity plus behavioural prepayment now.
		prepay := Money(float64(p.Amount) * *a.LoanPrepayRate)
		add(asOf, prepay, "loan_prepayment")
		add(p.MaturityDate, signed-prepay, "loan_repayment")
	}
	return flows
}

// BuildLadder expands all positions (sorted by ID for determinism) and
// buckets their flows relative to asOf.
func BuildLadder(positions map[string]*Position, a *Assumptions, asOf Date) *Ladder {
	l := &Ladder{AsOf: asOf}
	for _, p := range sortedPositions(positions) {
		for _, cf := range expandPosition(p, a, asOf) {
			l.Add(asOf.DaysTo(cf.Date), cf.Amount)
		}
	}
	return l
}

// HQLA computes the haircut-adjusted stock of high-quality liquid assets.
func HQLA(positions map[string]*Position, a *Assumptions) Money {
	var total Money
	for _, p := range sortedPositions(positions) {
		if p.Type == Security {
			total += Money(float64(p.Amount) * (1 - *a.SecurityHaircut))
		}
	}
	return total
}
