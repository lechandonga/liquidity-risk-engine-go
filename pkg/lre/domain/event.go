package domain

// OpType 表示批内对单笔头寸的操作类型。
type OpType string

const (
	// OpRegister 新登记：头寸 ID 已存在时按重复登记拒绝，绝不静默覆盖。
	OpRegister OpType = "REGISTER"
	// OpCorrect 显式更正：整体覆盖既有头寸，头寸必须已存在。
	OpCorrect OpType = "CORRECT"
)

// PositionOp 是写入批次内的单笔头寸操作。
type PositionOp struct {
	Type     OpType // 空值按 REGISTER 处理
	Position Position
}

// Batch 是写入的原子单元：要么整体对后续快照可见，要么完全不可见。
// BatchID 全局唯一；重复回放同一 BatchID 返回幂等结果，绝不重复计数。
type Batch struct {
	BatchID  string
	PostedAt Date // 业务登记日；允许回填历史日期，仅作为审计信息，不改变状态折叠
	Ops      []PositionOp
	// ReverseBatchID 非空时本批次为冲正批次：目标批次的全部头寸从状态中移除；
	// 冲正批次本身也可被再冲正（恢复），形成可审计的更正链。
	ReverseBatchID string
}

// AppliedBatch 记录已落入状态的批次及其来源归属，供冲正定位。
type AppliedBatch struct {
	BatchID        string
	PostedAt       Date
	Seq            int64 // 应用顺序，单调递增
	PositionIDs    []string
	ReverseBatchID string
	ReversedBy     string // 被哪个冲正批次撤销（空表示仍有效）
}
