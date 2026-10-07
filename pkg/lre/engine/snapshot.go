package engine

import (
	"fmt"
	"sort"
	"sync"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/scenario"
)

// Snapshot 是某个完整时点状态在某个已固定情景版本下的不可变计量结论。
// 一经发布永不改变；后续更正只产生新序号、新版本的快照。
type Snapshot struct {
	Seq         int64
	StateSeq    int64
	Scenario    string
	ScenarioVer int
	BaseDate    domain.Date
	HorizonDays int
	Flows       []domain.Flow // 展开后的压力现金流（可解释明细），按日期/头寸排序
	Metrics     domain.Metrics
	Positions   []domain.Position // 计算所用头寸的有序副本
}

// Compute 立即在“调用瞬间已发布的完整状态”上同步计算快照。
// 计算期间发生的写入不会影响结果（状态不可变 + 原子替换），也不会互相阻塞。
func (e *Engine) Compute(name string, version int, base domain.Date, horizon int) (*Snapshot, error) {
	r, err := e.scenarios.Get(name, version)
	if err != nil {
		return nil, err
	}
	st := e.state.Load()
	return e.computeOn(st, r, base, horizon)
}

func (e *Engine) computeOn(st *state, r *scenario.Resolved, base domain.Date, horizon int) (*Snapshot, error) {
	if horizon <= 0 {
		horizon = 1
	}
	positions := st.positionsSorted()
	flows, err := scenario.ExpandStress(r, positions, base, horizon)
	if err != nil {
		return nil, err
	}
	m := scenario.ComputeMetrics(r, positions, flows, base, horizon)
	// 防御性拷贝，保证快照持有者与引擎内部互不影响。
	flowCopy := append([]domain.Flow(nil), flows...)
	posCopy := append([]domain.Position(nil), positions...)
	return &Snapshot{
		Seq: st.seq, StateSeq: st.seq,
		Scenario: r.Name, ScenarioVer: r.Version,
		BaseDate: base, HorizonDays: horizon,
		Flows: flowCopy, Metrics: m, Positions: posCopy,
	}, nil
}

// ComputeRequest 是后台计算请求。
type ComputeRequest struct {
	ReqID       string
	Scenario    string
	Version     int
	BaseDate    domain.Date
	Horizon     int
	MinStateSeq int64 // 可选：至少等到状态序号达到该值再计算
}

// PublishAsync 在后台 goroutine 中计算并发布快照，立即返回。
// 计算从不可变状态读数，不持写锁，因此长计算不阻塞任何写入或查询；
// 完成后结果经 reply 返回（nil channel 表示“发后即忘”，快照仍入档）。
// 同一 (StateSeq, 情景, 版本, 基准日, 窗口) 已存在快照时直接复用，不重复计算。
func (e *Engine) PublishAsync(req ComputeRequest, reply chan<- *PublishResult) {
	go func() {
		e.computeSem <- struct{}{}
		defer func() { <-e.computeSem }()

		st := e.state.Load()
		for st.seq < req.MinStateSeq {
			e.stateWait(st.seq) // 条件变量等待，禁止 sleep 轮询；每次唤醒后重新读状态
			st = e.state.Load()
		}
		res := e.publishForState(st, req)
		if reply != nil {
			select {
			case reply <- res:
			default: // 调用方放弃接收时不泄漏 goroutine
			}
		}
	}()
}

// PublishResult 是后台计算的回执。
type PublishResult struct {
	Snapshot *Snapshot
	Err      error
	ReqID    string
}

type condHub struct {
	mu  sync.Mutex
	seq int64
	c   *sync.Cond
}

func (e *Engine) condHub() *condHub {
	e.condOnce.Do(func() {
		e.hub = &condHub{}
		e.hub.c = sync.NewCond(&e.hub.mu)
	})
	return e.hub
}

func (e *Engine) stateWait(afterSeq int64) {
	hub := e.condHub()
	hub.c.L.Lock()
	for hub.seq <= afterSeq {
		hub.c.Wait()
	}
	hub.c.L.Unlock()
}

func (e *Engine) stateNotify(seq int64) {
	hub := e.condHub()
	hub.c.L.Lock()
	if seq > hub.seq {
		hub.seq = seq
	}
	hub.c.Broadcast()
	hub.c.L.Unlock()
}

func (e *Engine) publishForState(st *state, req ComputeRequest) *PublishResult {
	r, err := e.scenarios.Get(req.Scenario, req.Version)
	if err != nil {
		return &PublishResult{Err: err, ReqID: req.ReqID}
	}
	key := snapKey(st.seq, r.Name, r.Version, req.BaseDate, req.Horizon)
	e.snapMu.Lock()
	if snap, ok := e.snapshots[key]; ok {
		e.snapMu.Unlock()
		return &PublishResult{Snapshot: snap, ReqID: req.ReqID}
	}
	e.snapMu.Unlock()

	snap, err := e.computeOn(st, r, req.BaseDate, req.Horizon)
	if err != nil {
		return &PublishResult{Err: err, ReqID: req.ReqID}
	}
	e.snapMu.Lock()
	if existing, ok := e.snapshots[key]; ok {
		snap = existing
	} else {
		e.snapshots[key] = snap
	}
	e.snapMu.Unlock()
	return &PublishResult{Snapshot: snap, ReqID: req.ReqID}
}

// GetSnapshot 取回已发布快照；不存在返回 nil,false。已发布快照永不被改写。
func (e *Engine) GetSnapshot(stateSeq int64, name string, version int, base domain.Date, horizon int) (*Snapshot, bool) {
	e.snapMu.Lock()
	defer e.snapMu.Unlock()
	snap, ok := e.snapshots[snapKey(stateSeq, name, version, base, horizon)]
	return snap, ok
}

// SnapshotKeys 返回已发布快照键的有序列表（审计/复现用）。
func (e *Engine) SnapshotKeys() []string {
	e.snapMu.Lock()
	defer e.snapMu.Unlock()
	keys := make([]string, 0, len(e.snapshots))
	for k := range e.snapshots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func snapKey(seq int64, name string, version int, base domain.Date, horizon int) string {
	return fmt.Sprintf("%d|%s|%d|%s|%d", seq, name, version, base, horizon)
}
