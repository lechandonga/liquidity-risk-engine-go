package ladder_test

import (
	"testing"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/ladder"
)

func tlog(t *testing.T, c, name, format string, args ...any) {
	t.Helper()
	t.Logf("[业务:%s][用例:%s] PASS - "+format, append([]any{c, name}, args...)...)
}

// TestOrderIndependence：同一头寸同一天的分桶结果不随登记顺序变化。
func TestOrderIndependence(t *testing.T) {
	const biz = "现金流分桶确定性"
	base := domain.MustDate(2026, 10, 8)

	positions := []domain.Position{
		{ID: "TD", Product: domain.TermDeposit, Direction: domain.Liability, Currency: "CNY",
			Counterparty: "a", Balance: 1_000_000, Rate: 0.03, ValueDate: base,
			MaturityDate: base.AddDays(30)},
		{ID: "LN", Product: domain.LoanReceivable, Direction: domain.Asset, Currency: "CNY",
			Counterparty: "b", Balance: 2_000_000, Rate: 0.05, ValueDate: base,
			MaturityDate: base.AddDays(60), Amortizing: true, Schedule: []domain.ScheduledPayment{
				{Date: base.AddDays(30), Amount: 1_000_000}, {Date: base.AddDays(60), Amount: 1_050_000},
			}},
		{ID: "SEC", Product: domain.SecurityHolding, Direction: domain.Asset, Currency: "CNY",
			Counterparty: "c", Balance: 700_000, ValueDate: base, MaturityDate: base.AddDays(90)},
	}

	build := func(order []int) map[string]int64 {
		l := ladder.New()
		for _, i := range order {
			for _, f := range ladder.ExpandContractual(positions[i]) {
				l.AddFlow(f)
			}
		}
		out := map[string]int64{}
		for _, sf := range l.SortedFlows() {
			out[sf.Date.String()] = sf.Amount
		}
		return out
	}
	a := build([]int{0, 1, 2})
	b := build([]int{2, 0, 1})
	c := build([]int{1, 2, 0})
	for date, v := range a {
		if b[date] != v || c[date] != v {
			t.Fatalf("日期 %s 分桶随顺序变化: %d vs %d vs %d", date, v, b[date], c[date])
		}
	}
	tlog(t, biz, "多产品合同展开顺序无关", "桶数=%d", len(a))

	// 任意时点净现金流查询。
	l := ladder.New()
	for _, p := range positions {
		for _, f := range ladder.ExpandContractual(p) {
			l.AddFlow(f)
		}
	}
	n30 := l.NetAt(base, base.AddDays(30))
	// 第30天：-定期本息 + 贷款第1期
	tdOut := -(1_000_000 + round(1_000_000*0.03*30.0/365.0))
	want30 := int64(tdOut) + 1_000_000
	if n30 != want30 {
		t.Fatalf("(base,+30] 净流期望 %d，实际 %d", want30, n30)
	}
	nAll := l.NetAt(domain.Date{}, base.AddDays(90))
	if nAll == 0 {
		t.Fatal("全窗口净现金流不应为 0")
	}
	tlog(t, biz, "任意时点净现金流查询", "第30天=%d 全窗口=%d", n30, nAll)
}

func round(x float64) int64 {
	if x >= 0 {
		return int64(x + 0.5)
	}
	return -int64(-x + 0.5)
}
