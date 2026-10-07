package liquidity

import (
	"fmt"
	"sync"
)

// Engine is the top-level facade: an event store, a scenario registry and a
// report cache. All state is in-memory and local; no external services.
type Engine struct {
	Store    *Store
	Scenario *ScenarioRegistry

	mu      sync.RWMutex
	reports map[reportKey]*Metrics // published, immutable conclusions
}

type reportKey struct {
	seq      uint64
	scenario string
	version  int
}

func NewEngine() *Engine {
	return &Engine{
		Store:    NewStore(),
		Scenario: NewScenarioRegistry(),
		reports:  map[reportKey]*Metrics{},
	}
}

// Ingest commits a batch of events atomically.
func (e *Engine) Ingest(b Batch) (ApplyResult, error) {
	return e.Store.Apply(b)
}

// Snapshot captures the current immutable ledger state.
func (e *Engine) Snapshot() Snapshot { return e.Store.Snapshot() }

// RunScenario computes (or returns the cached) stress metrics for a pinned
// scenario version against a specific snapshot. Results are keyed by
// (snapshot seq, scenario id, scenario version): a conclusion once published
// is immutable and exactly reproducible; new scenario versions or new
// snapshots produce new conclusions, never rewrite old ones.
func (e *Engine) RunScenario(snap Snapshot, scenarioID string, version int, asOf Date) (*Metrics, error) {
	key := reportKey{snap.Seq, scenarioID, version}
	e.mu.RLock()
	if m, ok := e.reports[key]; ok {
		e.mu.RUnlock()
		return m, nil
	}
	e.mu.RUnlock()

	a, err := e.Scenario.Resolve(scenarioID, version)
	if err != nil {
		return nil, err
	}
	// Compute outside any lock: the snapshot state is immutable, so long
	// calculations never block concurrent writes or queries.
	m := ComputeMetrics(snap.state.positions, a, asOf, scenarioID, version, snap.Seq)

	e.mu.Lock()
	if prev, ok := e.reports[key]; ok {
		e.mu.Unlock()
		return prev, nil
	}
	e.reports[key] = m
	e.mu.Unlock()
	return m, nil
}

// NetCashflowAt returns the net cashflow of the bucket containing date,
// under a pinned scenario version and snapshot.
func (e *Engine) NetCashflowAt(snap Snapshot, scenarioID string, version int, asOf, date Date) (Money, error) {
	m, err := e.RunScenario(snap, scenarioID, version, asOf)
	if err != nil {
		return 0, err
	}
	return m.Ladder.NetAt(date)
}

// Report returns a previously computed conclusion, or an error if the exact
// (snapshot, scenario, version) combination was never run.
func (e *Engine) Report(snapSeq uint64, scenarioID string, version int) (*Metrics, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	m, ok := e.reports[reportKey{snapSeq, scenarioID, version}]
	if !ok {
		return nil, fmt.Errorf("no published report for snapshot %d, scenario %s v%d", snapSeq, scenarioID, version)
	}
	return m, nil
}
