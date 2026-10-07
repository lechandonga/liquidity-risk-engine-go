package engine

import (
	"sort"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
)

// state 是某一完整时点的不可变状态：读路径拿到 *state 后无需任何锁即可计算，
// 写入通过复制-修改-整体替换（atomic.Pointer）发布，读永远不会看到半应用批次。
type state struct {
	seq       int64
	positions map[string]domain.Position // positionID -> 当前有效头寸
	// ownerBatch 记录每个头寸最后一次 UPSERT 的批次；冲正按批次撤销。
	ownerBatch map[string]string
	batches    map[string]domain.AppliedBatch
	// tombstoned 保存因冲正而移除的头寸内容，键为 positionID@被冲正批次ID，
	// 用于“对冲正再冲正”时确定性恢复。
	tombstoned map[string]domain.Position
	batchOrder []string // 批次应用顺序，保证回放/审计确定
}

func newState() *state {
	return &state{
		positions:  map[string]domain.Position{},
		ownerBatch: map[string]string{},
		batches:    map[string]domain.AppliedBatch{},
	}
}

func (s *state) clone() *state {
	c := &state{
		seq:        s.seq,
		positions:  make(map[string]domain.Position, len(s.positions)),
		ownerBatch: make(map[string]string, len(s.ownerBatch)),
		batches:    make(map[string]domain.AppliedBatch, len(s.batches)),
		tombstoned: make(map[string]domain.Position, len(s.tombstoned)),
		batchOrder: append([]string(nil), s.batchOrder...),
	}
	for k, v := range s.positions {
		c.positions[k] = v
	}
	for k, v := range s.ownerBatch {
		c.ownerBatch[k] = v
	}
	for k, v := range s.batches {
		c.batches[k] = v
	}
	for k, v := range s.tombstoned {
		c.tombstoned[k] = v
	}
	return c
}

// positionsSorted 返回按 ID 排序的头寸，保证计算输出确定。
func (s *state) positionsSorted() []domain.Position {
	ids := make([]string, 0, len(s.positions))
	for id := range s.positions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]domain.Position, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.positions[id])
	}
	return out
}

// applyResult 描述批次的处理结果。
type applyResult struct {
	accepted bool
	dup      bool // 已成功应用过：幂等返回，不重复计数
	rejects  domain.RejectList
	record   domain.AppliedBatch
}
