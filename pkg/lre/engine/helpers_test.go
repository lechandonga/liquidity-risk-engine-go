package engine_test

import (
	"testing"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
)

func fptr(v float64) *float64 { return &v }

// testLog 打印业务分组与用例通过情况，让单测日志直接体现覆盖的业务链路。
func testLog(t *testing.T, business, caseName string, format string, args ...any) {
	t.Helper()
	t.Logf("[业务:%s][用例:%s] PASS - "+format, append([]any{business, caseName}, args...)...)
}

func baseScenario(name string, version, horizon int, mutate func(*domain.ScenarioParams)) domain.ScenarioDef {
	p := domain.ScenarioParams{
		RunoffRate:            map[string]*float64{"DEFAULT": fptr(0.10), "RETAIL": fptr(0.05), "WHOLESALE": fptr(0.25)},
		EarlyRedemptionRate:   map[string]*float64{"DEFAULT": fptr(0.10)},
		WholesaleRolloverRate: map[string]*float64{"DEFAULT": fptr(0.40), "WHOLESALE": fptr(0.20)},
		DrawdownRate:          map[string]*float64{"DEFAULT": fptr(0.20)},
		PrepaymentRate:        map[string]*float64{"DEFAULT": fptr(0.03)},
		SecurityHaircut:       map[string]*float64{"DEFAULT": fptr(0.10), "GOVT": fptr(0.02)},
		LiquidateLevel:        map[string]string{"DEFAULT": "HQLA"},
		RunoffProfile:         map[string]string{"DEFAULT": "FLAT"},
		DepositInflowRate:     map[string]*float64{"DEFAULT": fptr(1)},
	}
	if mutate != nil {
		mutate(&p)
	}
	return domain.ScenarioDef{Name: name, Version: version, StressHorizonDays: horizon, Params: p}
}

func deposit(id string, bal int64, seg string) domain.Position {
	return domain.Position{ID: id, Product: domain.DemandDeposit, Direction: domain.Liability,
		Currency: "CNY", Counterparty: "cp-" + id, Segment: seg, Balance: bal,
		ValueDate: domain.MustDate(2026, 10, 8)}
}
