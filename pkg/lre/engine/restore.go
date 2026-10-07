package engine

import (
	"sort"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
)

// restorePositions 处理“对冲正批次再冲正”：把 reversalBatch 此前移除、
// 且当前仍不存在的头寸从墓碑区还原。还原是确定性的：内容来自第一次冲正时
// 保存的快照，绝不与之后的新登记/更正混算。
func restorePositions(next *state, reversalBatch domain.AppliedBatch) []string {
	originalID := reversalBatch.ReverseBatchID
	restored := make([]string, 0, len(reversalBatch.PositionIDs))
	for _, pid := range reversalBatch.PositionIDs {
		key := pid + "@" + originalID
		if _, live := next.positions[pid]; live {
			continue
		}
		pos, ok := next.tombstoned[key]
		if !ok {
			continue
		}
		next.positions[pid] = pos
		next.ownerBatch[pid] = originalID
		delete(next.tombstoned, key)
		restored = append(restored, pid)
	}
	sort.Strings(restored)
	return restored
}
