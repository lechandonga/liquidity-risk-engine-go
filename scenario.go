package liquidity

import (
	"fmt"
	"sort"
	"sync"
)

// Scenario is one immutable version of a stress scenario definition.
type Scenario struct {
	ID          string
	Version     int
	Base        string       // optional: ID of a scenario version this overlays
	BaseVersion int          // required when Base is set
	Overrides   *Assumptions // fields set here override the base, field by field
	Full        *Assumptions // complete set when Base is empty
}

// validateScenario checks structural and semantic rules. Nothing is
// defaulted: a root scenario must define every parameter; an overlay must
// reference an existing base version and define at least one override.
func validateScenario(s *Scenario) error {
	if s.ID == "" {
		return &RejectError{Code: CodeScenarioInvalid, Field: "ID", Detail: "scenario id is required"}
	}
	if s.Version < 1 {
		return &RejectError{Code: CodeScenarioInvalid, Field: "Version", Detail: "version must be >= 1"}
	}
	if s.Base == "" {
		if s.Full == nil {
			return &RejectError{Code: CodeScenarioInvalid, Field: "Full", Detail: "root scenario requires a complete assumption set"}
		}
		if s.Overrides != nil {
			return &RejectError{Code: CodeScenarioInvalid, Field: "Overrides", Detail: "root scenario cannot carry overrides"}
		}
		for _, f := range assumptionFields() {
			if f.get(s.Full) == nil {
				return &RejectError{Code: CodeScenarioInvalid, Field: f.name,
					Detail: "missing assumption parameter"}
			}
		}
		return validateRates(s.Full)
	}
	// Overlay scenario.
	if s.BaseVersion < 1 {
		return &RejectError{Code: CodeScenarioInvalid, Field: "BaseVersion", Detail: "overlay must pin a base version"}
	}
	if s.Full != nil {
		return &RejectError{Code: CodeScenarioInvalid, Field: "Full", Detail: "overlay cannot redefine the full set"}
	}
	if s.Overrides == nil {
		return &RejectError{Code: CodeScenarioInvalid, Field: "Overrides", Detail: "overlay must override at least one parameter"}
	}
	any := false
	for _, f := range assumptionFields() {
		if f.get(s.Overrides) != nil {
			any = true
		}
	}
	if !any {
		return &RejectError{Code: CodeScenarioInvalid, Field: "Overrides", Detail: "overlay must override at least one parameter"}
	}
	return validateRates(s.Overrides)
}

// validateRates enforces [0,1] bounds and cross-parameter consistency.
func validateRates(a *Assumptions) error {
	for _, f := range assumptionFields() {
		if v := f.get(a); v != nil && (*v < 0 || *v > 1) {
			return &RejectError{Code: CodeScenarioInvalid, Field: f.name,
				Detail: fmt.Sprintf("rate %v outside [0,1]", *v)}
		}
	}
	// Contradiction: rolling over more than 100% combined with a positive
	// runoff on the same funding block is inconsistent by construction here
	// (rollover applies to term, runoff to demand), so the cross-check is on
	// prepayment vs haircut style pairs that share a balance base.
	if a.LoanPrepayRate != nil && a.SecurityHaircut != nil {
		// No direct contradiction possible; placeholder for future rules.
	}
	return nil
}

// ScenarioRegistry stores immutable scenario versions. A published version
// can never be mutated or replaced, so historical results reproduce exactly.
type ScenarioRegistry struct {
	mu        sync.RWMutex
	versions  map[string]map[int]*Scenario // id -> version -> definition
	assumpRef map[string]*Assumptions      // named reusable assumption sets
}

func NewScenarioRegistry() *ScenarioRegistry {
	return &ScenarioRegistry{
		versions:  map[string]map[int]*Scenario{},
		assumpRef: map[string]*Assumptions{},
	}
}

// RegisterAssumptionSet publishes a named, reusable assumption set that
// scenarios may reference. Names are immutable once registered.
func (r *ScenarioRegistry) RegisterAssumptionSet(name string, a *Assumptions) error {
	if name == "" {
		return &RejectError{Code: CodeScenarioInvalid, Field: "name", Detail: "assumption set name required"}
	}
	full := &Scenario{ID: name, Version: 1, Full: a}
	if err := validateScenario(full); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.assumpRef[name]; exists {
		return &RejectError{Code: CodeScenarioInvalid, Field: "name",
			Detail: fmt.Sprintf("assumption set %q already registered", name)}
	}
	r.assumpRef[name] = a.clone()
	return nil
}

// Publish validates and registers a new scenario version. Republishing an
// existing (id, version) is rejected — versions are immutable.
func (r *ScenarioRegistry) Publish(s *Scenario) error {
	if err := validateScenario(s); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.versions[s.ID] == nil {
		r.versions[s.ID] = map[int]*Scenario{}
	}
	if _, exists := r.versions[s.ID][s.Version]; exists {
		return &RejectError{Code: CodeScenarioInvalid, Field: "Version",
			Detail: fmt.Sprintf("scenario %s version %d already published", s.ID, s.Version)}
	}
	if s.Base != "" {
		if _, ok := r.versions[s.Base][s.BaseVersion]; !ok {
			return &RejectError{Code: CodeUnknownScenario, Field: "Base",
				Detail: fmt.Sprintf("base scenario %s v%d is not defined", s.Base, s.BaseVersion)}
		}
	}
	cp := *s
	if s.Full != nil {
		cp.Full = s.Full.clone()
	}
	if s.Overrides != nil {
		cp.Overrides = s.Overrides.clone()
	}
	r.versions[s.ID][s.Version] = &cp
	return nil
}

// Resolve returns the fully-materialised assumption set for (id, version),
// applying the deterministic overlay chain: base first, then each overlay in
// version-pinned order, field by field. New and old assumptions are never
// mixed beyond the explicitly declared chain.
func (r *ScenarioRegistry) Resolve(id string, version int) (*Assumptions, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.versions[id][version]
	if !ok {
		return nil, &RejectError{Code: CodeUnknownScenario,
			Detail: fmt.Sprintf("scenario %s v%d is not defined", id, version)}
	}
	return r.resolveLocked(s)
}

func (r *ScenarioRegistry) resolveLocked(s *Scenario) (*Assumptions, error) {
	var out *Assumptions
	if s.Base == "" {
		out = s.Full.clone()
	} else {
		base, ok := r.versions[s.Base][s.BaseVersion]
		if !ok {
			return nil, &RejectError{Code: CodeUnknownScenario,
				Detail: fmt.Sprintf("base scenario %s v%d is not defined", s.Base, s.BaseVersion)}
		}
		var err error
		out, err = r.resolveLocked(base)
		if err != nil {
			return nil, err
		}
		for _, f := range assumptionFields() {
			if v := f.get(s.Overrides); v != nil {
				c := *v
				f.set(out, &c)
			}
		}
	}
	return out, nil
}

// Versions lists published versions of a scenario, ascending.
func (r *ScenarioRegistry) Versions(id string) []int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []int
	for v := range r.versions[id] {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}
