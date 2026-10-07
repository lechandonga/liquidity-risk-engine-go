package liquidity

import "fmt"

// Metrics is the stress-test result for one (snapshot, scenario version).
type Metrics struct {
	ScenarioID      string
	ScenarioVersion int
	AsOf            Date
	SnapshotSeq     uint64
	Ladder          *Ladder
	HQLA            Money
	NetOutflow30D   Money
	LCR             float64 // HQLA / net outflows 30d; +Inf if no outflows
	SurvivalDays    int     // days until cumulative liquidity (HQLA + cum net) < 0
}

// ComputeMetrics derives stressed net cashflow, LCR and survival horizon.
// Survival is evaluated day by day over a 400-day horizon using the bucket
// ladder plus the HQLA buffer as the starting stock.
func ComputeMetrics(positions map[string]*Position, a *Assumptions, asOf Date, scenarioID string, version int, seq uint64) *Metrics {
	ladder := BuildLadder(positions, a, asOf)
	hqla := HQLA(positions, a)
	outflow := ladder.NetOutflow30D()
	lcr := 0.0
	if outflow == 0 {
		lcr = -1 // sentinel: no stressed outflows, ratio undefined/infinite
	} else {
		lcr = float64(hqla) / float64(outflow)
	}
	// Survival: walk cumulative net flows bucket by bucket; the buffer must
	// cover any cumulative shortfall. Bucket end-day boundaries:
	bucketEnd := []int{0, 6, 13, 29, 89, 179, 364, 399}
	stock := hqla
	survival := 400
	cum := Money(0)
	for i := 0; i < BucketCount; i++ {
		cum += ladder.Flows[i]
		if stock+cum < 0 {
			survival = bucketEnd[i]
			break
		}
	}
	return &Metrics{
		ScenarioID: scenarioID, ScenarioVersion: version, AsOf: asOf,
		SnapshotSeq: seq, Ladder: ladder, HQLA: hqla,
		NetOutflow30D: outflow, LCR: lcr, SurvivalDays: survival,
	}
}

func (m *Metrics) String() string {
	lcr := "n/a (no stressed outflows)"
	if m.LCR >= 0 {
		lcr = fmt.Sprintf("%.2f", m.LCR)
	}
	return fmt.Sprintf("scenario=%s v%d asOf=%s snap=%d hqla=%d outflow30d=%d lcr=%s survival=%dd",
		m.ScenarioID, m.ScenarioVersion, m.AsOf, m.SnapshotSeq, m.HQLA, m.NetOutflow30D, lcr, m.SurvivalDays)
}
