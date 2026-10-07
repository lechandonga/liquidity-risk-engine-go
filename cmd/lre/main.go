// lre 是完全本地运行的演示命令：构造头寸 -> 发布两个版本的压力情景 ->
// 计算合同阶梯与压力指标 -> 演示冲正后旧快照不变、新版本快照反映更正。
// 用法： go run ./cmd/lre
package main

import (
	"fmt"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/engine"
)

func f64(v float64) *float64 { return &v }

func main() {
	base := domain.MustDate(2026, 10, 8)
	e := engine.New("CNY")

	mustApply(e, domain.Batch{BatchID: "B1", PostedAt: base, Ops: []domain.PositionOp{
		{Type: domain.OpRegister, Position: domain.Position{
			ID: "DEP-1", Product: domain.DemandDeposit, Direction: domain.Liability,
			Currency: "CNY", Counterparty: "cust-A", Segment: "RETAIL_STABLE",
			Balance: 1_000_000, Rate: 0.002, ValueDate: base}},
		{Type: domain.OpRegister, Position: domain.Position{
			ID: "WS-1", Product: domain.WholesaleFunding, Direction: domain.Liability,
			Currency: "CNY", Counterparty: "bank-B", Segment: "WHOLESALE_UNSECURED",
			Balance: 500_000, Rate: 0.025, ValueDate: base,
			MaturityDate: base.AddDays(10)}},
		{Type: domain.OpRegister, Position: domain.Position{
			ID: "SEC-1", Product: domain.SecurityHolding, Direction: domain.Asset,
			Currency: "CNY", Counterparty: "issuer-C", Segment: "GOVT",
			Balance: 800_000, ValueDate: base, MaturityDate: base.AddDays(365),
			HQLA: true, HQLALevel: "1"}},
	}})

	def := domain.ScenarioDef{
		Name: "BASE_STRESS", Version: 1, StressHorizonDays: 30,
		Params: domain.ScenarioParams{
			RunoffRate:            map[string]*float64{"DEFAULT": f64(0.05), "RETAIL_STABLE": f64(0.10)},
			WholesaleRolloverRate: map[string]*float64{"DEFAULT": f64(0.50)},
			DrawdownRate:          map[string]*float64{"DEFAULT": f64(0.10)},
			PrepaymentRate:        map[string]*float64{"DEFAULT": f64(0.02)},
			EarlyRedemptionRate:   map[string]*float64{"DEFAULT": f64(0.05)},
			SecurityHaircut:       map[string]*float64{"DEFAULT": f64(0.0), "GOVT": f64(0.02)},
			LiquidateLevel:        map[string]string{"DEFAULT": "HQLA"},
			RunoffProfile:         map[string]string{"DEFAULT": "DAY1"},
		},
	}
	if _, err := e.Scenarios().Publish(def); err != nil {
		panic(err)
	}

	fmt.Println("== 合同净现金流（任意时点查询） ==")
	fmt.Printf("(%s, %s] 净现金流: %d 分\n", base, base.AddDays(30), e.NetCashFlowAt(base, base.AddDays(30)))

	snap1, err := e.Compute("BASE_STRESS", 1, base, 30)
	if err != nil {
		panic(err)
	}
	printMetrics("情景 v1", snap1)

	// 新版本情景：流失率上调；v1 快照仍可原样复现。
	def2 := def
	def2.Version = 2
	def2.Params.RunoffRate = map[string]*float64{"DEFAULT": f64(0.20), "RETAIL_STABLE": f64(0.30)}
	if _, err := e.Scenarios().Publish(def2); err != nil {
		panic(err)
	}
	snap1Again, _ := e.Compute("BASE_STRESS", 1, base, 30)
	snap2, _ := e.Compute("BASE_STRESS", 2, base, 30)
	printMetrics("情景 v1（复现）", snap1Again)
	printMetrics("情景 v2", snap2)

	// 冲正批发融资批次条目（用更正演示），并展示幂等回放。
	mustApply(e, domain.Batch{BatchID: "B2", PostedAt: base, Ops: []domain.PositionOp{
		{Type: domain.OpCorrect, Position: domain.Position{
			ID: "WS-1", Product: domain.WholesaleFunding, Direction: domain.Liability,
			Currency: "CNY", Counterparty: "bank-B", Segment: "WHOLESALE_UNSECURED",
			Balance: 300_000, Rate: 0.025, ValueDate: base, MaturityDate: base.AddDays(10)}},
	}})
	r1, err := e.ApplyBatch(domain.Batch{BatchID: "B1"}) // 重复回放
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n重复回放 B1 => 幂等=%v，seq=%d（未重复计数）\n", r1.Idempotent, r1.Seq)
	snap3, _ := e.Compute("BASE_STRESS", 2, base, 30)
	printMetrics("更正后 v2（新状态快照）", snap3)
}

func mustApply(e *engine.Engine, b domain.Batch) {
	if _, err := e.ApplyBatch(b); err != nil {
		panic(err)
	}
}

func printMetrics(title string, s *engine.Snapshot) {
	m := s.Metrics
	fmt.Printf("\n== %s: %s@%d stateSeq=%d ==\n", title, s.Scenario, s.ScenarioVer, s.StateSeq)
	fmt.Printf("窗口净现金流=%d  流出=%d  流入=%d  HQLA=%d  LCR=%.2f%%  生存期=%d 天\n",
		m.TotalNetCashFlow, m.TotalOutflows, m.TotalInflows, m.HQLAAdjustedValue, m.LCR, m.SurvivalDays)
	if !m.SurvivalBreachDate.IsZero() {
		fmt.Printf("首次累计净流出: %s\n", m.SurvivalBreachDate)
	}
}
