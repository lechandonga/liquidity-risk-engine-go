// Package engine 提供本地流动性风险计量与压力测试引擎：
// 原子批次写入、幂等回放、回填与冲正、不可变版本快照、并发安全的异步计算。
package engine

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/scenario"
)

// Engine 是全部对外能力的入口；零值不可用，请使用 New。
type Engine struct {
	currency  string
	scenarios *scenario.Store

	writeMu sync.Mutex // 串行化批次应用，保证校验-折叠-发布原子
	state   atomic.Pointer[state]

	journal Journal // 可选：本地事件持久化（文件），nil 表示纯内存

	snapMu     sync.Mutex
	snapshots  map[string]*Snapshot // 快照键 -> 已发布快照，永不被后续写入篡改
	computeSem chan struct{}        // 限制并发重计算数量；不阻塞写入与查询
	condOnce   sync.Once
	hub        *condHub
}

// Option 配置引擎。
type Option func(*Engine)

// WithJournal 挂载本地持久化日志（纯本地文件，无外部服务）。
func WithJournal(j Journal) Option {
	return func(e *Engine) { e.journal = j }
}

// New 创建引擎；currency 为本引擎统一币种，头寸币种不符将被拒绝。
func New(currency string, opts ...Option) *Engine {
	e := &Engine{
		currency:   currency,
		scenarios:  scenario.NewStore(),
		snapshots:  map[string]*Snapshot{},
		computeSem: make(chan struct{}, 4),
	}
	e.state.Store(newState())
	for _, o := range opts {
		o(e)
	}
	return e
}

// Scenarios 暴露情景仓库用于发布/查询带版本情景。
func (e *Engine) Scenarios() *scenario.Store { return e.scenarios }

// ApplyResult 是 ApplyBatch 的对外结果。
type ApplyResult struct {
	BatchID string
	Seq     int64
	// Idempotent=true 表示该批次此前已成功应用（或已按冲正处理），
	// 本次为重复回放，状态未发生任何变化、未重复计数。
	Idempotent bool
}

// ApplyBatch 原子应用一个批次：
//   - 校验与折叠在同一临界区完成，任何一笔头寸非法则整批拒绝，无部分可见；
//   - 相同 BatchID 重复回放返回幂等结果；
//   - 回填历史日期（PostedAt 早于今天）允许，PostedAt 仅作审计信息；
//   - 冲正批次引用不存在/已冲正的目标时明确拒绝。
func (e *Engine) ApplyBatch(b domain.Batch) (ApplyResult, error) {
	e.writeMu.Lock()
	defer e.writeMu.Unlock()

	cur := e.state.Load()
	if existing, ok := cur.batches[b.BatchID]; ok {
		// 已存在即幂等回放：无论 UPSERT 还是冲正批次都不重复计数。
		return ApplyResult{BatchID: b.BatchID, Seq: existing.Seq, Idempotent: true}, nil
	}
	if b.BatchID == "" {
		return ApplyResult{}, &domain.RejectError{Code: domain.CodeMissingField, Field: "batch_id", Detail: "批次 ID 不能为空"}
	}

	next := cur.clone()
	next.seq++
	record := domain.AppliedBatch{BatchID: b.BatchID, PostedAt: b.PostedAt, Seq: next.seq}

	if b.ReverseBatchID != "" {
		target, ok := cur.batches[b.ReverseBatchID]
		if !ok {
			return ApplyResult{}, &domain.RejectError{Code: domain.CodeUnknownBatch, PosID: b.ReverseBatchID,
				Field: "reverse_batch_id", Detail: fmt.Sprintf("冲正目标批次 %q 不存在", b.ReverseBatchID)}
		}
		if target.ReversedBy != "" {
			return ApplyResult{}, &domain.RejectError{Code: domain.CodeBatchAlreadyReversed, PosID: b.BatchID,
				Field: "reverse_batch_id", Detail: fmt.Sprintf("批次 %q 已被 %q 冲正", b.ReverseBatchID, target.ReversedBy)}
		}
		// 若目标本身是冲正批次，本次操作等价于“恢复”其撤销的头寸；
		// 否则撤销目标批次登记的头寸：仅移除仍归属于该批次者，
		// 已被后续 CORRECT 更正的头寸保留其最新版本（更正优先于冲正）。
		var removed []string
		if target.ReverseBatchID != "" {
			removed = restorePositions(next, target)
		} else {
			for _, pid := range target.PositionIDs {
				if cur.ownerBatch[pid] == target.BatchID {
					next.tombstoned[pid+"@"+target.BatchID] = cur.positions[pid]
					delete(next.positions, pid)
					delete(next.ownerBatch, pid)
					removed = append(removed, pid)
				}
			}
		}
		sort.Strings(removed)
		record.ReverseBatchID = b.ReverseBatchID
		record.PositionIDs = removed
		tgt := target
		tgt.ReversedBy = b.BatchID
		next.batches[target.BatchID] = tgt
	} else {
		if len(b.Ops) == 0 {
			return ApplyResult{}, &domain.RejectError{Code: domain.CodeMissingField, PosID: b.BatchID,
				Field: "ops", Detail: "批次必须至少包含一笔操作"}
		}
		// 批内自查重：同一批次内重复登记同一头寸立即拒绝。
		seen := make(map[string]struct{}, len(b.Ops))
		rejects := domain.RejectList{}
		type validated struct {
			p  domain.Position
			ok bool
		}
		validatedOps := make([]validated, 0, len(b.Ops))
		for _, op := range b.Ops {
			p := op.Position
			if err := domain.ValidatePosition(p, e.currency); err != nil {
				rejects = append(rejects, asReject(err))
				continue
			}
			opType := op.Type
			if opType == "" {
				opType = domain.OpRegister
			}
			if opType != domain.OpRegister && opType != domain.OpCorrect {
				rejects = append(rejects, &domain.RejectError{Code: domain.CodeMissingField, PosID: p.ID,
					Field: "type", Detail: fmt.Sprintf("未知操作类型 %q", op.Type)})
				continue
			}
			if _, dup := seen[p.ID]; dup {
				rejects = append(rejects, &domain.RejectError{Code: domain.CodeDuplicatePosition, PosID: p.ID,
					Field: "id", Detail: fmt.Sprintf("头寸 %s 在批次 %s 内重复登记", p.ID, b.BatchID)})
				continue
			}
			seen[p.ID] = struct{}{}
			_, exists := cur.positions[p.ID]
			switch {
			case opType == domain.OpRegister && exists:
				rejects = append(rejects, &domain.RejectError{Code: domain.CodeDuplicatePosition, PosID: p.ID,
					Field: "id", Detail: fmt.Sprintf("头寸 %s 已由批次 %s 登记；重复登记被拒绝，如需更正请使用 CORRECT", p.ID, cur.ownerBatch[p.ID])})
			case opType == domain.OpCorrect && !exists:
				rejects = append(rejects, &domain.RejectError{Code: domain.CodeMissingField, PosID: p.ID,
					Field: "id", Detail: fmt.Sprintf("CORRECT 目标头寸 %s 不存在，无法更正", p.ID)})
			default:
				validatedOps = append(validatedOps, validated{p: p, ok: true})
			}
		}
		if len(rejects) > 0 {
			return ApplyResult{}, rejects
		}
		ids := make([]string, 0, len(validatedOps))
		for _, v := range validatedOps {
			next.positions[v.p.ID] = v.p
			next.ownerBatch[v.p.ID] = b.BatchID
			ids = append(ids, v.p.ID)
		}
		sort.Strings(ids)
		record.PositionIDs = ids
	}

	if e.journal != nil {
		if err := e.journal.Append(b); err != nil {
			return ApplyResult{}, fmt.Errorf("持久化批次 %s 失败: %w", b.BatchID, err)
		}
	}
	next.batches[b.BatchID] = record
	next.batchOrder = append(next.batchOrder, b.BatchID)
	e.state.Store(next)
	e.stateNotify(record.Seq)
	return ApplyResult{BatchID: b.BatchID, Seq: record.Seq}, nil
}

func asReject(err error) *domain.RejectError {
	if r, ok := err.(*domain.RejectError); ok {
		return r
	}
	return &domain.RejectError{Code: domain.CodeInvalidAmount, Detail: err.Error()}
}

// CurrentSeq 返回当前最新状态序号。
func (e *Engine) CurrentSeq() int64 { return e.state.Load().seq }

// Position 查询当前有效头寸（无锁读不可变状态）。
func (e *Engine) Position(id string) (domain.Position, bool) {
	p, ok := e.state.Load().positions[id]
	return p, ok
}

// Positions 返回当前有效头寸的有序快照副本。
func (e *Engine) Positions() []domain.Position { return e.state.Load().positionsSorted() }
