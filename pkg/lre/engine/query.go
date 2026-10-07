package engine

import (
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/ladder"
)

// ContractualLadder 在当前完整状态上构建合同现金流阶梯（无行为假设）。
func (e *Engine) ContractualLadder() *ladder.Ladder {
	st := e.state.Load()
	l := ladder.New()
	for _, p := range st.positionsSorted() {
		for _, f := range ladder.ExpandContractual(p) {
			l.AddFlow(f)
		}
	}
	return l
}

// NetCashFlowAt 返回当前状态下 (from, at] 区间的合同净现金流；
// from 为零值时从最早现金流日累计。任意时点可查询。
func (e *Engine) NetCashFlowAt(from, at domain.Date) int64 {
	return e.ContractualLadder().NetAt(from, at)
}

// BatchHistory 返回按应用顺序排列的批次审计记录副本。
func (e *Engine) BatchHistory() []domain.AppliedBatch {
	st := e.state.Load()
	out := make([]domain.AppliedBatch, 0, len(st.batchOrder))
	for _, id := range st.batchOrder {
		out = append(out, st.batches[id])
	}
	return out
}
