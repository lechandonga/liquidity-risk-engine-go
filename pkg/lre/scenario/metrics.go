package scenario

import (
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
)

// hqlaFactor 按巴塞尔口径给各级 HQLA 的计入系数。
var hqlaFactor = map[string]float64{"1": 1.0, "2A": 0.85, "2B": 0.50}

// ComputeMetrics 在已展开的压力现金流上汇总指标。
// flows 必须由同一 (Resolved, 头寸集合, baseDate, horizon) 产生，避免新旧假设混算。
func ComputeMetrics(r *Resolved, positions []domain.Position, flows []StressFlow, base domain.Date, horizon int) domain.Metrics {
	m := domain.Metrics{
		ScenarioName: r.Name, ScenarioVersion: r.Version,
		BaseDate: base, HorizonDays: horizon,
		NetCashFlowByDay: make([]int64, horizon),
		LCR:              -1,
	}
	for _, f := range flows {
		day := f.Date.DaysSince(base)
		if day < 1 || day > horizon {
			continue
		}
		m.NetCashFlowByDay[day-1] += f.Amount
		if f.Amount > 0 {
			m.TotalInflows += f.Amount
		} else {
			m.TotalOutflows += -f.Amount
		}
	}
	m.CumulativeNetCashFlow = make([]int64, horizon)
	var cum int64
	m.SurvivalDays = horizon
	for i, v := range m.NetCashFlowByDay {
		cum += v
		m.CumulativeNetCashFlow[i] = cum
		if cum < 0 && m.SurvivalDays == horizon {
			m.SurvivalDays = i // 第 i+1 天违约 => 完整存活 i 天
			m.SurvivalBreachDate = base.AddDays(i + 1)
		}
	}
	m.TotalNetCashFlow = cum
	if m.CumulativeNetCashFlow[horizon-1] >= 0 && m.SurvivalDays == horizon {
		m.SurvivalBreachDate = domain.Date{}
	}

	// HQLA：仅允许变现的高质量资产按级别计入；是否可变现由情景 LIQUIDATION 口径决定。
	for _, p := range positions {
		if p.Product != domain.SecurityHolding || !p.HQLA {
			continue
		}
		segment := segOr(p)
		level := r.requireString("liquidate_level", r.P.LiquidateLevel, segment, "NONE")
		if level == "NONE" {
			continue
		}
		haircut, err := r.requireRate("security_haircut", r.P.SecurityHaircut, segment)
		if err != nil {
			// 发布后情景参数不可变且 ComputeMetrics 前 ExpandStress 已强制校验；
			// 防御性处理：无法确认价值的资产不计入。
			continue
		}
		baseValue := p.MarketPrice
		if baseValue <= 0 {
			baseValue = p.Balance
		}
		factor := hqlaFactor[p.HQLALevel]
		m.HQLAAdjustedValue += signedRound(float64(baseValue) * factor * (1 - haircut))
	}

	gross := m.TotalOutflows - m.TotalInflows
	if gross > 0 {
		m.NetOutflow = gross
		m.LCR = float64(m.HQLAAdjustedValue) / float64(gross) * 100.0
	}
	return m
}
