package scenario

import (
	"fmt"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
)

// validateRates 校验单层参数：所有比率必须在合法区间内；
// 自相矛盾（如证券折价 >= 100%、无法识别的曲线/变现级别）直接拒绝。
func validateRates(name string, p domain.ScenarioParams) error {
	check := func(field string, m map[string]*float64, lo, hi float64) error {
		segments := make([]string, 0, len(m))
		for k := range m {
			segments = append(segments, k)
		}
		sortStrings(segments)
		for _, seg := range segments {
			v := m[seg]
			if v == nil {
				return reject(domain.CodeScenarioInvalid, name, field, "分层 %q 的参数为 nil", seg)
			}
			if *v < lo || *v > hi {
				return reject(domain.CodeScenarioInvalid, name, field, "分层 %q 的 %s=%v 超出区间 [%v,%v]", seg, field, *v, lo, hi)
			}
		}
		return nil
	}
	type rateSpec struct {
		field string
		m     map[string]*float64
		lo    float64
		hi    float64
	}
	for _, sp := range []rateSpec{
		{"runoff_rate", p.RunoffRate, 0, 1},
		{"early_redemption_rate", p.EarlyRedemptionRate, 0, 1},
		{"wholesale_rollover_rate", p.WholesaleRolloverRate, 0, 1},
		{"drawdown_rate", p.DrawdownRate, 0, 1},
		{"prepayment_rate", p.PrepaymentRate, 0, 1},
		{"security_haircut", p.SecurityHaircut, 0, 1 - 1e-9},
		{"deposit_inflow_rate", p.DepositInflowRate, 0, 1},
	} {
		if err := check(sp.field, sp.m, sp.lo, sp.hi); err != nil {
			return err
		}
	}
	validLevels := map[string]bool{"NONE": true, "HQLA": true, "ALL": true, "": true}
	for seg, v := range p.LiquidateLevel {
		if !validLevels[v] {
			return reject(domain.CodeScenarioInvalid, name, "liquidate_level",
				"分层 %q 的 liquidate_level=%q 非法，允许 NONE/HQLA/ALL", seg, v)
		}
	}
	validProfiles := map[string]bool{"FLAT": true, "DAY1": true, "": true}
	for seg, v := range p.RunoffProfile {
		if !validProfiles[v] {
			return reject(domain.CodeScenarioInvalid, name, "runoff_profile",
				"分层 %q 的 runoff_profile=%q 非法，允许 FLAT/DAY1", seg, v)
		}
	}
	return nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// requireRate 在计算阶段按分层取参数：先精确匹配 segment，再回退 "DEFAULT"；
// 两者都缺失属于“引用未定义假设”，明确拒绝而非按零处理。
func (r *Resolved) requireRate(field string, m map[string]*float64, segment string) (float64, error) {
	if v, ok := m[segment]; ok && v != nil {
		return *v, nil
	}
	if v, ok := m["DEFAULT"]; ok && v != nil {
		return *v, nil
	}
	return 0, &domain.RejectError{
		Code: domain.CodeScenarioInvalid, PosID: r.Name, Field: field,
		Detail: fmt.Sprintf("情景 %s@%d 缺少分层 %q（且无 DEFAULT 兜底）的 %s 假设", r.Name, r.Version, segment, field),
	}
}

func (r *Resolved) requireString(field string, m map[string]string, segment string, fallback string) string {
	if v, ok := m[segment]; ok && v != "" {
		return v
	}
	if v, ok := m["DEFAULT"]; ok && v != "" {
		return v
	}
	return fallback
}
