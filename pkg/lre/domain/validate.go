package domain

import "math"

// ValidatePosition 校验单笔头寸的字段完整性与口径一致性。
// 任何问题都返回带原因码的 RejectError，绝不静默修正或按零处理。
func ValidatePosition(p Position, engineCCY string) error {
	if p.ID == "" {
		return reject(CodeMissingField, p.ID, "id", "头寸 ID 不能为空")
	}
	if p.Currency == "" {
		return reject(CodeMissingField, p.ID, "currency", "币种不能为空")
	}
	if engineCCY != "" && p.Currency != engineCCY {
		return reject(CodeCurrencyMismatch, p.ID, "currency", "头寸币种 %s 与引擎币种 %s 不一致", p.Currency, engineCCY)
	}
	if p.Counterparty == "" {
		return reject(CodeMissingField, p.ID, "counterparty", "交易对手不能为空")
	}
	switch p.Product {
	case DemandDeposit, TermDeposit, WholesaleFunding, UndrawnCreditLine, SecurityHolding, LoanReceivable:
	default:
		return reject(CodeUnknownProduct, p.ID, "product", "未知产品类型 %q", p.Product)
	}
	want := directionFor(p.Product)
	if p.Direction == "" {
		return reject(CodeMissingField, p.ID, "direction", "方向不能为空")
	}
	if p.Direction != want {
		return reject(CodeDirectionMismatch, p.ID, "direction", "产品 %s 的口径必须为 %s，实际为 %s", p.Product, want, p.Direction)
	}
	if p.Balance <= 0 {
		return reject(CodeInvalidAmount, p.ID, "balance", "本金/面值必须 > 0，实际为 %d", p.Balance)
	}
	if p.MarketPrice < 0 {
		return reject(CodeInvalidAmount, p.ID, "market_price", "市值不能为负：%d", p.MarketPrice)
	}
	if p.Rate < 0 {
		return reject(CodeInvalidAmount, p.ID, "rate", "利率不能为负：%v", p.Rate)
	}
	if p.Rate > 10 {
		return reject(CodeInvalidAmount, p.ID, "rate", "利率 %v 超出合理范围（0..1），疑似口径错误", p.Rate)
	}
	if p.NoticeDays < 0 {
		return reject(CodeInvalidTerm, p.ID, "notice_days", "通知期不能为负：%d", p.NoticeDays)
	}
	if p.ValueDate.IsZero() {
		return reject(CodeMissingField, p.ID, "value_date", "起息日不能为空")
	}
	if err := validateProductTerms(p); err != nil {
		return err
	}
	return nil
}

func directionFor(k ProductKind) Direction {
	switch k {
	case DemandDeposit, TermDeposit, WholesaleFunding:
		return Liability
	case UndrawnCreditLine:
		return Contingent
	case SecurityHolding, LoanReceivable:
		return Asset
	}
	return ""
}

func validateProductTerms(p Position) error {
	switch p.Product {
	case DemandDeposit:
		// 无固定期限；若填写到期日必须不早于起息日。
		if !p.MaturityDate.IsZero() && p.MaturityDate.Before(p.ValueDate) {
			return reject(CodeInvalidTerm, p.ID, "maturity_date", "活期存款到期日 %s 早于起息日 %s", p.MaturityDate, p.ValueDate)
		}
	case TermDeposit, WholesaleFunding, SecurityHolding:
		if p.MaturityDate.IsZero() {
			return reject(CodeMissingField, p.ID, "maturity_date", "%s 必须填写到期日", p.Product)
		}
		if !p.MaturityDate.After(p.ValueDate) {
			return reject(CodeInvalidTerm, p.ID, "maturity_date", "到期日 %s 必须晚于起息日 %s", p.MaturityDate, p.ValueDate)
		}
		if p.Amortizing {
			return reject(CodeInvalidTerm, p.ID, "amortizing", "%s 不支持摊还计划", p.Product)
		}
		if p.Product == SecurityHolding {
			if !p.HQLA && p.HQLALevel != "" {
				return reject(CodeMissingField, p.ID, "hqla", "标注了 HQLA 级别 %s 但 HQLA=false", p.HQLALevel)
			}
			if p.HQLA {
				switch p.HQLALevel {
				case "1", "2A", "2B":
				default:
					return reject(CodeInvalidTerm, p.ID, "hqla_level", "HQLA 级别必须为 1/2A/2B，实际为 %q", p.HQLALevel)
				}
			}
		}
	case UndrawnCreditLine:
		if !p.MaturityDate.IsZero() && p.MaturityDate.Before(p.ValueDate) {
			return reject(CodeInvalidTerm, p.ID, "maturity_date", "授信到期日 %s 早于起息日 %s", p.MaturityDate, p.ValueDate)
		}
	case LoanReceivable:
		if p.MaturityDate.IsZero() {
			return reject(CodeMissingField, p.ID, "maturity_date", "贷款必须填写到期日")
		}
		if !p.MaturityDate.After(p.ValueDate) {
			return reject(CodeInvalidTerm, p.ID, "maturity_date", "贷款到期日 %s 必须晚于起息日 %s", p.MaturityDate, p.ValueDate)
		}
		if p.Amortizing {
			if len(p.Schedule) == 0 {
				return reject(CodeMissingField, p.ID, "schedule", "摊还贷款必须提供还款计划")
			}
			var sum int64
			prev := p.ValueDate
			for i, pay := range p.Schedule {
				if pay.Amount <= 0 {
					return reject(CodeInvalidAmount, p.ID, "schedule", "第 %d 期还款金额必须 > 0", i+1)
				}
				if pay.Date.IsZero() {
					return reject(CodeMissingField, p.ID, "schedule", "第 %d 期还款日期不能为空", i+1)
				}
				if i > 0 && !pay.Date.After(prev) {
					return reject(CodeInvalidTerm, p.ID, "schedule", "还款计划日期必须严格递增，冲突于 %s", pay.Date)
				}
				if !pay.Date.After(p.ValueDate) {
					return reject(CodeInvalidTerm, p.ID, "schedule", "还款日 %s 必须晚于起息日 %s", pay.Date, p.ValueDate)
				}
				if pay.Date.After(p.MaturityDate) {
					return reject(CodeInvalidTerm, p.ID, "schedule", "还款日 %s 晚于到期日 %s", pay.Date, p.MaturityDate)
				}
				sum += pay.Amount
				prev = pay.Date
			}
			if sum > math.MaxInt64/2 {
				return reject(CodeInvalidAmount, p.ID, "schedule", "还款计划合计金额溢出")
			}
		}
	}
	return nil
}
