package domain

import "fmt"

// 拒绝原因码：调用方可以按 Code 区分脏数据类别，错误信息中携带头寸 ID。
type Code string

const (
	CodeMissingField         Code = "MISSING_FIELD"          // 必填字段缺失
	CodeInvalidAmount        Code = "INVALID_AMOUNT"         // 金额非法（<=0、溢出等）
	CodeInvalidTerm          Code = "INVALID_TERM"           // 期限/日期非法或与到期日矛盾
	CodeUnknownProduct       Code = "UNKNOWN_PRODUCT"        // 未知产品类型
	CodeDirectionMismatch    Code = "DIRECTION_MISMATCH"     // 方向与产品口径不符
	CodeDuplicatePosition    Code = "DUPLICATE_POSITION"     // 同一头寸重复登记
	CodeCurrencyMismatch     Code = "CURRENCY_MISMATCH"      // 头寸币种与引擎币种不一致
	CodeUnknownBatch         Code = "UNKNOWN_BATCH"          // 冲正引用了不存在的批次
	CodeBatchAlreadyReversed Code = "BATCH_ALREADY_REVERSED" // 批次已冲正
	CodeBatchAlreadyApplied  Code = "BATCH_ALREADY_APPLIED"  // 批次已成功应用（幂等回放）
	CodeScenarioInvalid      Code = "SCENARIO_INVALID"       // 情景参数缺失/自相矛盾
	CodeScenarioNotFound     Code = "SCENARIO_NOT_FOUND"     // 引用了未发布/未定义的情景
	CodeInvalidDate          Code = "INVALID_DATE"           // 日期非法
)

// RejectError 携带可区分的拒绝原因。PosID 为空时表示错误与单笔头寸无关。
type RejectError struct {
	Code   Code
	PosID  string
	Field  string
	Detail string
}

func (e *RejectError) Error() string {
	id := e.PosID
	if id == "" {
		id = "-"
	}
	field := e.Field
	if field == "" {
		field = "-"
	}
	return fmt.Sprintf("[%s] position=%s field=%s: %s", e.Code, id, field, e.Detail)
}

func reject(code Code, posID, field, format string, args ...any) error {
	return &RejectError{Code: code, PosID: posID, Field: field, Detail: fmt.Sprintf(format, args...)}
}

// RejectList 聚合一个批次内所有头寸的拒绝原因，逐笔可区分，不静默丢弃。
type RejectList []*RejectError

func (l RejectList) Error() string {
	if len(l) == 0 {
		return ""
	}
	return fmt.Sprintf("%d 笔头寸被拒绝，首个原因: %s", len(l), l[0].Error())
}
