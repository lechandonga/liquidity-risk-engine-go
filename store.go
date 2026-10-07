package liquidity

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// EventKind classifies ledger events.
type EventKind string

const (
	EventBook    EventKind = "BOOK"    // register a new position
	EventCorrect EventKind = "CORRECT" // replace a position's terms (new version)
	EventReverse EventKind = "REVERSE" // reverse (tombstone) a position
)

// Event is a single ledger entry. BusinessDate may be backfilled (in the
// past); the event still lands in a new immutable state version.
type Event struct {
	ID           string
	Kind         EventKind
	Position     Position // payload for BOOK / CORRECT
	PositionID   string   // target for CORRECT / REVERSE
	BusinessDate Date
}

// Batch is the atomic unit of ingestion: a batch is applied in full or not
// at all, so readers never observe a half-applied batch.
type Batch struct {
	BatchID  string
	Events   []Event
	Recorded time.Time
}

// ApplyResult reports what a batch commit did.
type ApplyResult struct {
	BatchID   string
	Seq       uint64 // new state sequence
	Applied   int
	Duplicate bool // batch was already applied; replay is a no-op
}

// state is an immutable materialised view: positions plus the event IDs and
// batch IDs already applied (for idempotent replay).
type state struct {
	seq       uint64
	positions map[string]*Position
	eventIDs  map[string]struct{}
	batchIDs  map[string]struct{}
}

func (s *state) clone() *state {
	out := &state{
		seq:       s.seq + 1,
		positions: make(map[string]*Position, len(s.positions)),
		eventIDs:  make(map[string]struct{}, len(s.eventIDs)),
		batchIDs:  make(map[string]struct{}, len(s.batchIDs)),
	}
	for k, v := range s.positions {
		cp := *v
		out.positions[k] = &cp
	}
	for k := range s.eventIDs {
		out.eventIDs[k] = struct{}{}
	}
	for k := range s.batchIDs {
		out.batchIDs[k] = struct{}{}
	}
	return out
}

// Store is the append-only event log plus MVCC materialised state. Writers
// serialise on a mutex; readers load an immutable state pointer atomically
// and never block writers or each other.
type Store struct {
	mu      sync.Mutex
	current atomic.Pointer[state]
	log     []Batch // append-only audit trail
}

func NewStore() *Store {
	s := &Store{}
	s.current.Store(&state{
		positions: map[string]*Position{},
		eventIDs:  map[string]struct{}{},
		batchIDs:  map[string]struct{}{},
	})
	return s
}

// Apply validates and commits a batch atomically. Replaying an already
// applied BatchID is a reported no-op, never a double count. On any
// validation error the whole batch is rejected and state is untouched.
func (s *Store) Apply(b Batch) (ApplyResult, error) {
	if b.BatchID == "" {
		return ApplyResult{}, &RejectError{Code: CodeMissingField, Field: "BatchID", Detail: "batch id is required"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.current.Load()
	if _, dup := cur.batchIDs[b.BatchID]; dup {
		return ApplyResult{BatchID: b.BatchID, Seq: cur.seq, Duplicate: true}, nil
	}
	next := cur.clone()
	for i := range b.Events {
		if err := applyEvent(next, &b.Events[i]); err != nil {
			return ApplyResult{}, fmt.Errorf("batch %s rejected: %w", b.BatchID, err)
		}
	}
	next.batchIDs[b.BatchID] = struct{}{}
	s.current.Store(next) // single atomic publish: readers see all or nothing
	s.log = append(s.log, b)
	return ApplyResult{BatchID: b.BatchID, Seq: next.seq, Applied: len(b.Events)}, nil
}

func applyEvent(st *state, e *Event) error {
	if e.ID == "" {
		return &RejectError{Code: CodeMissingField, Field: "Event.ID", Detail: "event id is required"}
	}
	if _, dup := st.eventIDs[e.ID]; dup {
		return &RejectError{Code: CodeDuplicatePosition, Field: "Event.ID",
			Detail: fmt.Sprintf("event %s already applied", e.ID)}
	}
	switch e.Kind {
	case EventBook:
		p := e.Position
		if err := p.validate(); err != nil {
			return err
		}
		if _, exists := st.positions[p.ID]; exists {
			return &RejectError{Code: CodeDuplicatePosition, Field: "Position.ID",
				Detail: fmt.Sprintf("position %s already registered", p.ID)}
		}
		cp := p
		st.positions[p.ID] = &cp
	case EventCorrect:
		if e.PositionID == "" {
			return &RejectError{Code: CodeMissingField, Field: "PositionID", Detail: "correction target required"}
		}
		if _, exists := st.positions[e.PositionID]; !exists {
			return &RejectError{Code: CodeUnknownPosition, Field: "PositionID",
				Detail: fmt.Sprintf("cannot correct unknown position %s", e.PositionID)}
		}
		p := e.Position
		p.ID = e.PositionID // corrections never re-key a position
		if err := p.validate(); err != nil {
			return err
		}
		st.positions[e.PositionID] = &p
	case EventReverse:
		if e.PositionID == "" {
			return &RejectError{Code: CodeMissingField, Field: "PositionID", Detail: "reversal target required"}
		}
		if _, exists := st.positions[e.PositionID]; !exists {
			return &RejectError{Code: CodeUnknownPosition, Field: "PositionID",
				Detail: fmt.Sprintf("cannot reverse unknown position %s", e.PositionID)}
		}
		delete(st.positions, e.PositionID)
	default:
		return &RejectError{Code: CodeMissingField, Field: "Kind",
			Detail: fmt.Sprintf("unknown event kind %q", e.Kind)}
	}
	st.eventIDs[e.ID] = struct{}{}
	return nil
}

// Snapshot is an immutable, point-in-time view of the ledger. Published
// snapshots are never mutated by later corrections — corrections only ever
// produce a new snapshot sequence.
type Snapshot struct {
	Seq   uint64
	state *state
}

// Snapshot captures the current immutable state. The call is O(1) and never
// blocks writers for longer than a pointer load.
func (s *Store) Snapshot() Snapshot {
	st := s.current.Load()
	return Snapshot{Seq: st.seq, state: st}
}

// Positions returns the position set of this snapshot (defensive copies).
func (s Snapshot) Positions() map[string]*Position {
	out := make(map[string]*Position, len(s.state.positions))
	for k, v := range s.state.positions {
		cp := *v
		out[k] = &cp
	}
	return out
}

// LogLen returns the number of committed batches (audit trail length).
func (s *Store) LogLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.log)
}
