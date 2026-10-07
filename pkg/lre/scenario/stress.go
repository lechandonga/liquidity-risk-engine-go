package scenario

import (
	"sort"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/ladder"
)

// StressFlow 是压力展开后的单笔现金流，携带口径标签用于解释。
type StressFlow = domain.Flow

// ExpandStress 把一批头寸在指定已解析情景下展开到 [base+1, base+horizon] 窗口内。
// 合同现金流与行为现金流的口径清晰分开；任何分层假设缺失都返回拒绝错误。
func ExpandStress(r *Resolved, positions []domain.Position, base domain.Date, horizon int) ([]StressFlow, error) {
	var out []StressFlow
	// 按头寸 ID 排序，保证同输入下输出完全确定。
	positions = append([]domain.Position(nil), positions...)
	sort.Slice(positions, func(i, j int) bool { return positions[i].ID < positions[j].ID })

	for _, p := range positions {
		segment := p.Segment
		if segment == "" {
			segment = "DEFAULT"
		}
		// 1) 窗口内合同现金流（贷款摊还/到期本息/证券到期等）。
		contractual := ladder.ExpandContractual(p)
		for _, f := range contractual {
			days := f.Date.DaysSince(base)
			if days >= 1 && days <= horizon {
				out = append(out, f)
			}
		}
		// 2) 行为假设现金流。
		switch p.Product {
		case domain.DemandDeposit:
			rate, err := r.requireRate("runoff_rate", r.P.RunoffRate, segment)
			if err != nil {
				return nil, err
			}
			profile := r.requireString("runoff_profile", r.P.RunoffProfile, segment, "FLAT")
			out = append(out, amortizeBehavior(p, base, horizon, -p.Balance, rate, domain.BasisRunoff, profile)...)
		case domain.TermDeposit:
			rate, err := r.requireRate("early_redemption_rate", r.P.EarlyRedemptionRate, segment)
			if err != nil {
				return nil, err
			}
			// 仅对窗口之后才到期的定期存款施加提前支取；窗口内到期的部分已计入合同。
			if p.MaturityDate.After(base.AddDays(horizon)) && rate > 0 {
				profile := r.requireString("runoff_profile", r.P.RunoffProfile, segment, "FLAT")
				out = append(out, amortizeBehavior(p, base, horizon, -p.Balance, rate, domain.BasisEarlyRedeem, profile)...)
			}
		case domain.WholesaleFunding:
			roll, err := r.requireRate("wholesale_rollover_rate", r.P.WholesaleRolloverRate, segment)
			if err != nil {
				return nil, err
			}
			days := p.MaturityDate.DaysSince(base)
			if days >= 1 && days <= horizon {
				// 到期不滚续部分构成净流出（合同 -Balance 已含全部，ROLLOVER 记正向抵消）。
				if roll > 0 {
					out = append(out, domain.Flow{PosID: p.ID, Date: p.MaturityDate.AddDays(p.NoticeDays),
						Amount: signedRound(float64(p.Balance) * roll), Product: p.Product, Segment: segment, Basis: domain.BasisRollover})
				}
			}
		case domain.UndrawnCreditLine:
			rate, err := r.requireRate("drawdown_rate", r.P.DrawdownRate, segment)
			if err != nil {
				return nil, err
			}
			profile := r.requireString("runoff_profile", r.P.RunoffProfile, segment, "FLAT")
			limit := horizon
			if !p.MaturityDate.IsZero() {
				if d := p.MaturityDate.DaysSince(base); d < limit {
					limit = d
				}
			}
			if limit > 0 {
				out = append(out, amortizeBehavior(p, base, limit, -p.Balance, rate, domain.BasisDrawdown, profile)...)
			}
		case domain.LoanReceivable:
			prepay, err := r.requireRate("prepayment_rate", r.P.PrepaymentRate, segment)
			if err != nil {
				return nil, err
			}
			if prepay > 0 {
				profile := r.requireString("runoff_profile", r.P.RunoffProfile, segment, "FLAT")
				out = append(out, amortizeBehavior(p, base, horizon, p.Balance, prepay, domain.BasisPrepayment, profile)...)
			}
		case domain.SecurityHolding:
			if err := appendSecurityLiquidation(&out, r, p, base, horizon, segment); err != nil {
				return nil, err
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Date.Equal(out[j].Date) {
			return out[i].Date.Before(out[j].Date)
		}
		if out[i].PosID != out[j].PosID {
			return out[i].PosID < out[j].PosID
		}
		return out[i].Basis < out[j].Basis
	})
	return out, nil
}

func appendSecurityLiquidation(out *[]StressFlow, r *Resolved, p domain.Position, base domain.Date, horizon int, segment string) error {
	level := r.requireString("liquidate_level", r.P.LiquidateLevel, segment, "NONE")
	allowed := level == "ALL" || (level == "HQLA" && p.HQLA)
	if !allowed {
		return nil
	}
	haircut, err := r.requireRate("security_haircut", r.P.SecurityHaircut, segment)
	if err != nil {
		return err
	}
	// 仅当证券在窗口内尚未到期时才在第 1 天变现（窗口内到期的按合同兑付，避免重复计数）。
	if p.MaturityDate.After(base.AddDays(horizon)) || p.MaturityDate.Equal(base.AddDays(horizon)) {
		baseValue := p.MarketPrice
		if baseValue <= 0 {
			baseValue = p.Balance
		}
		value := signedRound(float64(baseValue) * (1 - haircut))
		*out = append(*out, domain.Flow{PosID: p.ID, Date: base.AddDays(1 + p.NoticeDays),
			Amount: value, Product: p.Product, Segment: segment, Basis: domain.BasisLiquidation})
	}
	return nil
}

// amortizeBehavior 把总量 |baseAmount|*rate 的行为现金流按曲线分摊到窗口各天。
// FLAT：均摊到每一天，余数按确定规则加到最后一天（保证分桶与顺序无关、总额精确）。
// DAY1：全部落在第 1 天。
func amortizeBehavior(p domain.Position, base domain.Date, horizon int, baseAmount int64, rate float64, basis domain.FlowBasis, profile string) []StressFlow {
	total := signedRound(float64(baseAmount) * rate)
	if total == 0 || horizon <= 0 {
		return nil
	}
	sign := int64(1)
	if total < 0 {
		sign = -1
	}
	abs := total * sign
	var flows []StressFlow
	put := func(day int, amt int64) {
		if amt == 0 {
			return
		}
		flows = append(flows, domain.Flow{PosID: p.ID, Date: base.AddDays(day),
			Amount: amt * sign, Product: p.Product, Basis: basis,
			Segment: segOr(p)})
	}
	switch profile {
	case "DAY1":
		put(1, abs)
	default: // FLAT
		per := abs / int64(horizon)
		rem := abs - per*int64(horizon)
		for day := 1; day <= horizon; day++ {
			amt := per
			if day == horizon {
				amt += rem
			}
			put(day, amt)
		}
	}
	return flows
}

func segOr(p domain.Position) string {
	if p.Segment == "" {
		return "DEFAULT"
	}
	return p.Segment
}

func signedRound(x float64) int64 {
	if x >= 0 {
		return int64(x + 0.5)
	}
	return -int64(-x + 0.5)
}
