// Package ladder 负责把头寸按合同现金流与行为假设展开为按日历日的现金流阶梯，
// 并提供确定性的聚合与任意时点净现金流查询。
package ladder

import (
	"math"
	"sort"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
)

// ExpandContractual 展开一笔头寸的纯合同现金流（不含任何行为假设）。
// 结果按日期升序返回；同一头寸同一天若产生多笔同口径现金流，按固定规则求和，
// 保证分桶结果与登记顺序无关。
func ExpandContractual(p domain.Position) []domain.Flow {
	var flows []domain.Flow
	add := func(d domain.Date, amt int64, basis domain.FlowBasis) {
		if d.IsZero() || amt == 0 {
			return
		}
		flows = append(flows, domain.Flow{
			PosID: p.ID, Date: d, Amount: amt, Product: p.Product,
			Segment: seg(p), Basis: basis,
		})
	}
	expiry := func() domain.Date {
		d := p.MaturityDate
		if p.NoticeDays > 0 {
			d = d.AddDays(p.NoticeDays)
		}
		return d
	}

	switch p.Product {
	case domain.DemandDeposit:
		// 合同口径：活期存款随时可被提取，但不预设具体提取日，合同阶梯为空
		// （压力流出由行为假设 RUNOFF 产生）。
	case domain.TermDeposit, domain.WholesaleFunding:
		// 到期还本（流出）+ 到期一次性付息（流出）。
		d := expiry()
		add(d, -p.Balance, domain.BasisContractual)
		interest := roundMoney(float64(p.Balance) * p.Rate * yearFraction(p.ValueDate, p.MaturityDate))
		add(d, -interest, domain.BasisContractual)
	case domain.SecurityHolding:
		// 证券到期按面值兑付（流入）。
		add(expiry(), p.Balance, domain.BasisContractual)
	case domain.LoanReceivable:
		if p.Amortizing {
			for _, pay := range p.Schedule {
				add(pay.Date, pay.Amount, domain.BasisContractual)
			}
		} else {
			d := expiry()
			add(d, p.Balance, domain.BasisContractual)
			interest := roundMoney(float64(p.Balance) * p.Rate * yearFraction(p.ValueDate, p.MaturityDate))
			add(d, interest, domain.BasisContractual)
		}
	}
	return mergeSameDay(flows)
}

func seg(p domain.Position) string {
	if p.Segment == "" {
		return "DEFAULT"
	}
	return p.Segment
}

// yearFraction 以 ACT/365F 计算期限年数，所有计算方使用同一确定规则。
func yearFraction(from, to domain.Date) float64 {
	days := to.DaysSince(from)
	if days < 0 {
		days = 0
	}
	return float64(days) / 365.0
}

// roundMoney 四舍五入到最近的整数货币单位（半数远离零），保证跨平台一致。
func roundMoney(x float64) int64 {
	if x >= 0 {
		return int64(math.Floor(x + 0.5))
	}
	return -int64(math.Floor(-x + 0.5))
}

// mergeSameDay 把同一头寸同一天、同口径的现金流合并求和。
// 合并前先排序，结果只取决于输入集合，与登记/产生顺序无关。
func mergeSameDay(flows []domain.Flow) []domain.Flow {
	sort.SliceStable(flows, func(i, j int) bool {
		if !flows[i].Date.Equal(flows[j].Date) {
			return flows[i].Date.Before(flows[j].Date)
		}
		if flows[i].Basis != flows[j].Basis {
			return flows[i].Basis < flows[j].Basis
		}
		return flows[i].Amount < flows[j].Amount
	})
	out := flows[:0]
	for _, f := range flows {
		if n := len(out); n > 0 && out[n-1].PosID == f.PosID && out[n-1].Date.Equal(f.Date) && out[n-1].Basis == f.Basis {
			out[n-1].Amount += f.Amount
			continue
		}
		out = append(out, f)
	}
	return append([]domain.Flow(nil), out...)
}
