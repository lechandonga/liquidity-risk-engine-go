package domain

// ProductKind 标识头寸产品类型，决定现金流展开口径。
type ProductKind string

const (
	// DemandDeposit  活期存款：负债，行为假设驱动逐期流失（流出）。
	DemandDeposit ProductKind = "DEMAND_DEPOSIT"
	// TermDeposit 定期存款：负债，到期还本付息；支持提前支取假设。
	TermDeposit ProductKind = "TERM_DEPOSIT"
	// WholesaleFunding 批发性融资：负债，到期偿付，假设滚续比例。
	WholesaleFunding ProductKind = "WHOLESALE_FUNDING"
	// UndrawnCreditLine 已承诺未提取授信：表外或有负债，压力下被提取（流出）。
	UndrawnCreditLine ProductKind = "UNDRAWN_CREDIT_LINE"
	// SecurityHolding 持有证券：资产，可到期兑付或在压力下折价变现（流入）。
	SecurityHolding ProductKind = "SECURITY_HOLDING"
	// LoanReceivable 贷款回款：资产，按摊还计划流入，支持提前还款假设。
	LoanReceivable ProductKind = "LOAN_RECEIVABLE"
)

// Direction 为资产/负债口径。
type Direction string

const (
	Asset      Direction = "ASSET"
	Liability  Direction = "LIABILITY"
	Contingent Direction = "CONTINGENT" // 表外或有项（未提取授信）
)

// ScheduledPayment 是合同约定的还款/付息计划（本金与利息合计，金额为正）。
type ScheduledPayment struct {
	Date   Date
	Amount int64 // 最小货币单位（如分），必须 > 0
}

// Position 是一笔头寸/合同的完整描述。金额一律使用 int64 最小货币单位。
type Position struct {
	ID           string // 全局唯一；重复登记必须被拒绝
	Product      ProductKind
	Direction    Direction
	Currency     string
	Counterparty string
	// Segment 用于情景参数分层（如 RETAIL_STABLE / WHOLESALE_SECURED 等），
	// 缺省值为 "DEFAULT"。
	Segment string

	// Balance 当前本金/面值（>0）。
	Balance int64
	// Rate 合同年利率，如 0.03；仅对到期付息类产品必需。
	Rate float64

	// ValueDate 起息日（合同现金流展开的锚点）。
	ValueDate Date
	// MaturityDate 到期日；活期/无固定期限产品可不填。
	MaturityDate Date
	// NoticeDays 通知期天数（>=0）：现金流落在满足通知期后的首个可执行日。
	NoticeDays int

	// Amortizing 为 true 时按 Schedule 摊还；false 时到期一次性还本付息。
	Amortizing bool
	Schedule   []ScheduledPayment

	// HQLA 仅证券使用：是否计入优质流动性资产（一级/二级由 HQLALevel 标识）。
	HQLA bool
	// HQLALevel: 1 / 2A / 2B，决定 LCR 计入比例。
	HQLALevel string
	// MarketPrice 仅证券使用：当前市值（>0 时作为变现基数；缺省回退到 Balance）。
	MarketPrice int64
}

// Flow 是展开后的单笔合同/行为现金流。
type Flow struct {
	PosID   string
	Date    Date
	Amount  int64 // 带符号：流入为正，流出为负
	Product ProductKind
	Segment string
	Basis   FlowBasis // 口径标签，保证结论可解释
}

// FlowBasis 标明现金流的合同或行为口径。
type FlowBasis string

const (
	BasisContractual FlowBasis = "CONTRACTUAL" // 合同现金流（还本/付息/摊还/证券到期）
	BasisRunoff      FlowBasis = "RUNOFF"      // 行为假设：存款流失
	BasisEarlyRedeem FlowBasis = "EARLY_REDEMPTION"
	BasisDrawdown    FlowBasis = "DRAWDOWN"    // 授信提取
	BasisPrepayment  FlowBasis = "PREPAYMENT"  // 贷款提前还款
	BasisRollover    FlowBasis = "ROLLOVER"    // 批发融资滚续（抵消到期流出）
	BasisLiquidation FlowBasis = "LIQUIDATION" // 证券折价变现
)
