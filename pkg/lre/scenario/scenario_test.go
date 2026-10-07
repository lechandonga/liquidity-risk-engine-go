package scenario_test

import (
	"testing"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/engine"
)

func slog(t *testing.T, c, name, format string, args ...any) {
	t.Helper()
	t.Logf("[业务:%s][用例:%s] PASS - "+format, append([]any{c, name}, args...)...)
}

// TestScenarioVersioningAndRepro 覆盖情景缺失/矛盾/未定义引用的拒绝、
// 父子继承覆盖规则、版本不可变与历史结论复现。
func TestScenarioVersioningAndRepro(t *testing.T) {
	const biz = "情景版本与复现"
	e := engine.New("CNY")
	base := domain.MustDate(2026, 10, 8)

	_, err := e.ApplyBatch(domain.Batch{BatchID: "B1", PostedAt: base, Ops: []domain.PositionOp{
		{Type: domain.OpRegister, Position: deposit2("DEP", 1_000_000, "RETAIL")},
	}})
	if err != nil {
		t.Fatal(err)
	}

	store := e.Scenarios()

	// 1) 参数越界 / 自相矛盾必须拒绝。
	bad := validDef("S", 1, 30)
	bad.Params.RunoffRate = map[string]*float64{"DEFAULT": ptr(1.5)}
	if _, err := store.Publish(bad); code(err) != domain.CodeScenarioInvalid {
		t.Fatalf("流失率 1.5 必须拒绝，实际 %v", err)
	}
	bad2 := validDef("S", 1, 30)
	bad2.Params.SecurityHaircut = map[string]*float64{"DEFAULT": ptr(1)}
	if _, err := store.Publish(bad2); code(err) != domain.CodeScenarioInvalid {
		t.Fatalf("100%% 折价必须拒绝，实际 %v", err)
	}
	bad3 := validDef("S", 1, 0)
	if _, err := store.Publish(bad3); code(err) != domain.CodeScenarioInvalid {
		t.Fatalf("窗口为 0 必须拒绝，实际 %v", err)
	}
	bad4 := validDef("S", 1, 30)
	bad4.Params.RunoffProfile = map[string]string{"DEFAULT": "RAMP"}
	if _, err := store.Publish(bad4); code(err) != domain.CodeScenarioInvalid {
		t.Fatalf("非法行为曲线必须拒绝，实际 %v", err)
	}
	slog(t, biz, "参数缺失与自相矛盾拒绝", "越界比率/100%%折价/非法曲线均拒绝")

	// 2) 引用未定义父情景必须拒绝。
	orphan := validDef("CHILD", 1, 30)
	orphan.Parent = "GHOST"
	if _, err := store.Publish(orphan); code(err) != domain.CodeScenarioNotFound {
		t.Fatalf("未定义父情景必须拒绝，实际 %v", err)
	}
	// 继承与版本固定：
	// CHILD@1 以 ParentVersion=0 继承父“发布时刻的最新版本”，之后父发布新版本，
	// CHILD@1 的解析结果必须仍固定在旧父版本上，不与新假设混算。
	parent1 := validDef("P", 1, 30)
	parent1.Params.RunoffRate["DEFAULT"] = ptr(0.1)
	if _, err := store.Publish(parent1); err != nil {
		t.Fatal(err)
	}
	child1 := validDef("CHILD", 1, 30)
	child1.Parent = "P"                                                 // ParentVersion=0 => 发布时最新 = P@1
	child1.Params.RunoffRate = map[string]*float64{"RETAIL": ptr(0.05)} // DEFAULT 由父继承
	rChild1, err := store.Publish(child1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rChild1.Chain) != 2 || rChild1.Chain[1].Version != 1 {
		t.Fatalf("CHILD@1 必须固定到 P@1，实际链 %+v", rChild1.Chain)
	}
	parent2 := validDef("P", 2, 30)
	parent2.Params.RunoffRate["DEFAULT"] = ptr(0.99)
	if _, err := store.Publish(parent2); err != nil {
		t.Fatal(err)
	}
	gotChild, err := store.Get("CHILD", 1)
	if err != nil {
		t.Fatal(err)
	}
	if gotChild.Chain[1].Version != 1 || *gotChild.P.RunoffRate["DEFAULT"] == 0.99 {
		t.Fatalf("CHILD@1 不得被 P@2 悄悄改写: chain=%+v", gotChild.Chain)
	}
	// 新发布的 CHILD@2（仍写 ParentVersion=0）才应固定到 P@2。
	child2 := validDef("CHILD", 2, 30)
	child2.Parent = "P"
	child2.Params.RunoffRate = map[string]*float64{"RETAIL": ptr(0.05)}
	rChild2, err := store.Publish(child2)
	if err != nil {
		t.Fatal(err)
	}
	if rChild2.Chain[1].Version != 2 || *rChild2.P.RunoffRate["DEFAULT"] != 0.99 {
		t.Fatalf("CHILD@2 必须固定到 P@2，实际 %+v", rChild2.Chain)
	}
	slog(t, biz, "未定义引用拒绝与父版本固定", "CHILD@1->P@1 不受 P@2 影响，CHILD@2->P@2")

	// 3) 已发布版本不可变。
	if _, err := store.Publish(validDef("P", 1, 20)); code(err) != domain.CodeScenarioInvalid {
		t.Fatalf("重复发布同名同版本必须拒绝，实际 %v", err)
	}

	// 4) 分层覆盖规则 + 历史版本复现。
	v1 := validDef("STRESS", 1, 30)
	if _, err := store.Publish(v1); err != nil {
		t.Fatal(err)
	}
	snap1, err := e.Compute("STRESS", 1, base, 30)
	if err != nil {
		t.Fatal(err)
	}

	v2 := validDef("STRESS", 2, 30)
	v2.Params.RunoffRate["RETAIL"] = ptr(0.50) // 仅覆盖 RETAIL 分层，DEFAULT 不变
	if _, err := store.Publish(v2); err != nil {
		t.Fatal(err)
	}
	snap1b, err := e.Compute("STRESS", 1, base, 30)
	if err != nil {
		t.Fatal(err)
	}
	snap2, err := e.Compute("STRESS", 2, base, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !sameMetrics(snap1.Metrics, snap1b.Metrics) {
		t.Fatalf("v1 历史结论无法原样复现:\n%+v\n%+v", snap1.Metrics, snap1b.Metrics)
	}
	if sameMetrics(snap1.Metrics, snap2.Metrics) {
		t.Fatal("v2 覆盖 RETAIL 分层后结果应与 v1 不同")
	}
	if snap1.Metrics.ScenarioVersion != 1 || snap2.Metrics.ScenarioVersion != 2 {
		t.Fatal("快照必须固定情景版本号")
	}
	slog(t, biz, "版本切换与历史复现",
		"v1净流=%d v2净流=%d（RETAIL流失覆盖生效，v1不变）",
		snap1.Metrics.TotalNetCashFlow, snap2.Metrics.TotalNetCashFlow)

	// 5) 缺分层且无 DEFAULT 兜底，计算时必须拒绝（引用未定义假设）。
	miss := validDef("MISS", 1, 30)
	miss.Params.RunoffRate = map[string]*float64{"RETAIL": ptr(0.1)} // 无 DEFAULT
	if _, err := store.Publish(miss); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplyBatch(domain.Batch{BatchID: "B-CORP", PostedAt: base, Ops: []domain.PositionOp{
		{Type: domain.OpRegister, Position: deposit2("DEP-CORP", 1_000, "CORP")},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Compute("MISS", 1, base, 30); code(err) != domain.CodeScenarioInvalid {
		t.Fatalf("缺少分层假设必须拒绝，实际 %v", err)
	}
	slog(t, biz, "未定义分层假设拒绝", "CORP 存款在无 DEFAULT 兜底时拒绝")

	// 6) 生存期与 LCR 可计算。
	snap3, err := e.Compute("STRESS", 2, base, 30)
	if err != nil {
		t.Fatal(err)
	}
	if snap3.Metrics.SurvivalDays < 0 || snap3.Metrics.SurvivalDays > 30 {
		t.Fatalf("生存期越界: %d", snap3.Metrics.SurvivalDays)
	}
	slog(t, biz, "指标计算", "生存期=%d天 净流出=%d HQLA=%d",
		snap3.Metrics.SurvivalDays, snap3.Metrics.NetOutflow, snap3.Metrics.HQLAAdjustedValue)
}

func validDef(name string, version, horizon int) domain.ScenarioDef {
	return domain.ScenarioDef{
		Name: name, Version: version, StressHorizonDays: horizon,
		Params: domain.ScenarioParams{
			RunoffRate:            map[string]*float64{"DEFAULT": ptr(0.1), "RETAIL": ptr(0.05)},
			EarlyRedemptionRate:   map[string]*float64{"DEFAULT": ptr(0.05)},
			WholesaleRolloverRate: map[string]*float64{"DEFAULT": ptr(0.5)},
			DrawdownRate:          map[string]*float64{"DEFAULT": ptr(0.1)},
			PrepaymentRate:        map[string]*float64{"DEFAULT": ptr(0.02)},
			SecurityHaircut:       map[string]*float64{"DEFAULT": ptr(0.05)},
			LiquidateLevel:        map[string]string{"DEFAULT": "NONE"},
			RunoffProfile:         map[string]string{"DEFAULT": "FLAT"},
		},
	}
}

func deposit2(id string, bal int64, seg string) domain.Position {
	return domain.Position{ID: id, Product: domain.DemandDeposit, Direction: domain.Liability,
		Currency: "CNY", Counterparty: "cp", Segment: seg, Balance: bal,
		ValueDate: domain.MustDate(2026, 10, 8)}
}

func ptr(v float64) *float64 { return &v }

func code(err error) domain.Code {
	if err == nil {
		return ""
	}
	if r, ok := err.(*domain.RejectError); ok {
		return r.Code
	}
	if rl, ok := err.(domain.RejectList); ok {
		return rl[0].Code
	}
	return ""
}

func sameMetrics(a, b domain.Metrics) bool {
	return a.TotalNetCashFlow == b.TotalNetCashFlow &&
		a.TotalInflows == b.TotalInflows && a.TotalOutflows == b.TotalOutflows &&
		a.HQLAAdjustedValue == b.HQLAAdjustedValue && a.SurvivalDays == b.SurvivalDays
}
