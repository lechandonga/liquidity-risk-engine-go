package domain

// ScenarioDef 是一个具名、带版本号的压力情景。
// 已发布的定义不可变；修改只能产生新版本，历史计算结果据此复现。
type ScenarioDef struct {
	Name    string
	Version int
	// Parent 为基础情景名：本情景的缺省参数继承自 Parent 的指定版本，
	// 本情景显式给出的字段执行覆盖。ParentVersion=0 表示父情景的最新版本
	// 仅用于发布期校验；解析结果会固定到具体版本（ResolvedParent）。
	Parent        string
	ParentVersion int
	Params        ScenarioParams
	// StressHorizonDays 压力测试窗口（自然日），必须 > 0。
	StressHorizonDays int
}

// ScenarioParams 中指针为 nil 表示“本层未定义”。
// 分层：按 Position.Segment 索引，键 "DEFAULT" 为兜底；找不到分层即拒绝。
type ScenarioParams struct {
	// RunoffRate[segment]：活期存款在压力窗口内的累计流失比例 [0,1]。
	RunoffRate map[string]*float64
	// EarlyRedemptionRate[segment]：定期存款在到期前被提前支取的比例 [0,1]。
	EarlyRedemptionRate map[string]*float64
	// WholesaleRolloverRate[segment]：到期批发融资的滚续比例 [0,1]。
	WholesaleRolloverRate map[string]*float64
	// DrawdownRate[segment]：未提取授信在压力窗口内的累计提取比例 [0,1]。
	DrawdownRate map[string]*float64
	// PrepaymentRate[segment]：贷款在压力窗口内的累计提前还款比例 [0,1]。
	PrepaymentRate map[string]*float64
	// SecurityHaircut[segment]：证券变现折价比例 [0,1)。
	SecurityHaircut map[string]*float64
	// LiquidateLevel[segment]: "NONE" / "HQLA" / "ALL"，决定哪些证券可变现。
	LiquidateLevel map[string]string
	// RunoffProfile[segment] 命名行为曲线："FLAT"（窗口内线性）或
	// "DAY1"（第 1 天全部发生）。缺省为 FLAT。
	RunoffProfile map[string]string
	// DepositInflowRate[segment]：贷款等资产产生的合同现金在压力下的回收率 [0,1]。
	DepositInflowRate map[string]*float64
}

// Metrics 是一个情景下的计量结果。
type Metrics struct {
	ScenarioName    string
	ScenarioVersion int
	BaseDate        Date
	HorizonDays     int

	// NetCashFlowByDay 压力窗口内每日净现金流（最小货币单位）。
	NetCashFlowByDay []int64
	// CumulativeNetCashFlow 第 i 天的累计净现金流。
	CumulativeNetCashFlow []int64
	// TotalNetCashFlow 窗口合计净现金流。
	TotalNetCashFlow int64

	// HQLAAdjustedValue 折价后的优质流动性资产变现价值。
	HQLAAdjustedValue int64
	// TotalOutflows / TotalInflows 窗口内压力流出/流入绝对值合计。
	TotalOutflows int64
	TotalInflows  int64
	// NetOutflow = max(流出 - 流入, 0)，LCR 分母。
	NetOutflow int64
	// LCR 百分比（如 118 表示 118%）；无净流出时记为 -1 表示不适用。
	LCR float64

	// SurvivalDays 自基准日起累计净现金流保持 >= 0 的完整天数；
	// 整个窗口均为负则为 0；整个窗口均非负则等于窗口长度。
	SurvivalDays int
	// SurvivalBreachDate 首个累计净流出的日期；生存期内不违约时为零值。
	SurvivalBreachDate Date
}
