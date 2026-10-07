package engine_test

import (
	"testing"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/engine"
)

const businessDirty = "脏数据与重复登记"

// TestDirtyDataAndDuplicate 覆盖：缺字段、金额/期限非法、方向口径不符、
// 批内/跨批重复登记、显式更正与脏数据的区分；并验证整批原子拒绝。
func TestDirtyDataAndDuplicate(t *testing.T) {
	e := engine.New("CNY")
	base := domain.MustDate(2026, 10, 8)

	cases := []struct {
		name string
		pos  domain.Position
		want domain.Code
	}{
		{"缺少交易对手",
			deposit("P1", 1000, "DEFAULT") /* mutate below */, domain.CodeMissingField},
		{"金额为零",
			deposit("P2", 0, "DEFAULT"), domain.CodeInvalidAmount},
		{"金额为负",
			deposit("P3", -5, "DEFAULT"), domain.CodeInvalidAmount},
		{"币种不符",
			func() domain.Position { p := deposit("P4", 100, "DEFAULT"); p.Currency = "USD"; return p }(),
			domain.CodeCurrencyMismatch},
		{"未知产品",
			func() domain.Position { p := deposit("P5", 100, "DEFAULT"); p.Product = "CRYPTO"; return p }(),
			domain.CodeUnknownProduct},
		{"方向口径不符(存款标成资产)",
			func() domain.Position { p := deposit("P6", 100, "DEFAULT"); p.Direction = domain.Asset; return p }(),
			domain.CodeDirectionMismatch},
		{"定期存款缺到期日",
			domain.Position{ID: "P7", Product: domain.TermDeposit, Direction: domain.Liability,
				Currency: "CNY", Counterparty: "cp7", Balance: 100, ValueDate: base},
			domain.CodeMissingField},
		{"到期日早于起息日",
			domain.Position{ID: "P8", Product: domain.TermDeposit, Direction: domain.Liability,
				Currency: "CNY", Counterparty: "cp8", Balance: 100, ValueDate: base,
				MaturityDate: base.AddDays(-1)}, domain.CodeInvalidTerm},
		{"摊还计划为空",
			domain.Position{ID: "P9", Product: domain.LoanReceivable, Direction: domain.Asset,
				Currency: "CNY", Counterparty: "cp9", Balance: 100, ValueDate: base,
				MaturityDate: base.AddDays(30), Amortizing: true}, domain.CodeMissingField},
		{"摊还计划日期乱序",
			domain.Position{ID: "P10", Product: domain.LoanReceivable, Direction: domain.Asset,
				Currency: "CNY", Counterparty: "cp10", Balance: 100, ValueDate: base,
				MaturityDate: base.AddDays(30), Amortizing: true, Schedule: []domain.ScheduledPayment{
					{Date: base.AddDays(20), Amount: 50}, {Date: base.AddDays(10), Amount: 50},
				}}, domain.CodeInvalidTerm},
		{"HQLA级别非法",
			domain.Position{ID: "P11", Product: domain.SecurityHolding, Direction: domain.Asset,
				Currency: "CNY", Counterparty: "cp11", Balance: 100, ValueDate: base,
				MaturityDate: base.AddDays(30), HQLA: true, HQLALevel: "3"},
			domain.CodeInvalidTerm},
		{"缺少起息日",
			domain.Position{ID: "P12", Product: domain.DemandDeposit, Direction: domain.Liability,
				Currency: "CNY", Counterparty: "cp12", Balance: 100}, domain.CodeMissingField},
	}
	cases[0].pos.Counterparty = ""

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.ApplyBatch(domain.Batch{BatchID: "B-" + tc.pos.ID, PostedAt: base,
				Ops: []domain.PositionOp{{Type: domain.OpRegister, Position: tc.pos}}})
			if err == nil {
				t.Fatalf("期望拒绝错误码 %s，但批次被接受", tc.want)
			}
			if rl, ok := err.(domain.RejectList); ok {
				found := false
				for _, r := range rl {
					if r.Code == tc.want {
						found = true
					}
				}
				if !found {
					t.Fatalf("错误原因不匹配：期望 %s，实际 %v", tc.want, rl)
				}
			} else if r, ok := err.(*domain.RejectError); ok {
				if r.Code != tc.want {
					t.Fatalf("错误原因不匹配：期望 %s，实际 %s", tc.want, r.Code)
				}
			} else {
				t.Fatalf("非预期错误类型: %T %v", err, err)
			}
			if _, exists := e.Position(tc.pos.ID); exists {
				t.Fatalf("被拒绝头寸 %s 不应进入状态", tc.pos.ID)
			}
			testLog(t, businessDirty, tc.name, "拒绝原因码=%s 头寸未入状态", tc.want)
		})
	}

	// 整批原子：一批中混有合法与非法头寸，必须全部不可见。
	_, err := e.ApplyBatch(domain.Batch{BatchID: "B-ATOMIC", PostedAt: base, Ops: []domain.PositionOp{
		{Type: domain.OpRegister, Position: deposit("GOOD", 100, "DEFAULT")},
		{Type: domain.OpRegister, Position: deposit("BAD", -1, "DEFAULT")},
	}})
	if err == nil {
		t.Fatal("含脏数据的批次必须被拒绝")
	}
	if _, ok := e.Position("GOOD"); ok {
		t.Fatal("原子性破坏：同批合法头寸不应在批次被拒后可见")
	}
	testLog(t, businessDirty, "整批原子拒绝", "合法/非法混合批次整体不可见")

	// 批内重复登记。
	_, err = e.ApplyBatch(domain.Batch{BatchID: "B-DUP1", PostedAt: base, Ops: []domain.PositionOp{
		{Type: domain.OpRegister, Position: deposit("DUP", 100, "DEFAULT")},
		{Type: domain.OpRegister, Position: deposit("DUP", 200, "DEFAULT")},
	}})
	if codeOf(err) != domain.CodeDuplicatePosition {
		t.Fatalf("批内重复登记必须拒绝，实际 %v", err)
	}
	testLog(t, businessDirty, "批内重复登记", "拒绝重复 ID=DUP")

	// 跨批重复登记 vs 显式更正。
	ok1, err := e.ApplyBatch(domain.Batch{BatchID: "B-DUP2", PostedAt: base, Ops: []domain.PositionOp{
		{Type: domain.OpRegister, Position: deposit("X", 100, "DEFAULT")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.ApplyBatch(domain.Batch{BatchID: "B-DUP3", PostedAt: base, Ops: []domain.PositionOp{
		{Type: domain.OpRegister, Position: deposit("X", 200, "DEFAULT")},
	}})
	if codeOf(err) != domain.CodeDuplicatePosition {
		t.Fatalf("跨批未声明更正的重复登记必须拒绝，实际 %v", err)
	}
	_, err = e.ApplyBatch(domain.Batch{BatchID: "B-DUP4", PostedAt: base, Ops: []domain.PositionOp{
		{Type: domain.OpCorrect, Position: deposit("X", 200, "DEFAULT")},
	}})
	if err != nil {
		t.Fatalf("显式 CORRECT 必须被接受，实际 %v", err)
	}
	if p, _ := e.Position("X"); p.Balance != 200 {
		t.Fatalf("更正后余额应为 200，实际 %d", p.Balance)
	}
	// CORRECT 不存在的头寸必须拒绝。
	_, err = e.ApplyBatch(domain.Batch{BatchID: "B-DUP5", PostedAt: base, Ops: []domain.PositionOp{
		{Type: domain.OpCorrect, Position: deposit("GHOST", 1, "DEFAULT")},
	}})
	if codeOf(err) != domain.CodeMissingField {
		t.Fatalf("CORRECT 不存在头寸必须拒绝，实际 %v", err)
	}
	testLog(t, businessDirty, "跨批重复与显式更正", "重复登记拒绝、CORRECT 覆盖成功 seq=%d", ok1.Seq)
}

func codeOf(err error) domain.Code {
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
