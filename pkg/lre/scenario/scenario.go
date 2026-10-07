// Package scenario 管理带版本的压力情景：定义校验、父子继承的确定性解析、
// 压力行为现金流生成以及 LCR / 生存期等指标计算。
package scenario

import (
	"fmt"
	"sort"
	"sync"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
)

// Store 保存已发布情景。发布后不可变；同名新版本独立共存，计算可引用任意旧版本。
type Store struct {
	mu       sync.RWMutex
	defs     map[string]map[int]domain.ScenarioDef // name -> version -> def
	resolved map[string]map[int]*Resolved
	latest   map[string]int
}

func NewStore() *Store {
	return &Store{
		defs:     map[string]map[int]domain.ScenarioDef{},
		resolved: map[string]map[int]*Resolved{},
		latest:   map[string]int{},
	}
}

// Resolved 是继承链展开后的有效参数与叠加规则结果，计算只依赖它。
type Resolved struct {
	Name    string
	Version int
	// Chain 记录从子到根的版本链，用于审计与“不混算”保证。
	Chain []ScenarioRef
	P     domain.ScenarioParams
}

// ScenarioRef 标识情景链上的一环。
type ScenarioRef struct {
	Name    string
	Version int
}

// Publish 校验并发布一个情景版本。已发布的同名同版本不可修改，返回错误。
func (s *Store) Publish(def domain.ScenarioDef) (*Resolved, error) {
	if def.Name == "" {
		return nil, reject(domain.CodeScenarioInvalid, "", "name", "情景名不能为空")
	}
	if def.Version <= 0 {
		return nil, reject(domain.CodeScenarioInvalid, def.Name, "version", "版本号必须 > 0，实际为 %d", def.Version)
	}
	if def.StressHorizonDays <= 0 {
		return nil, reject(domain.CodeScenarioInvalid, def.Name, "stress_horizon_days", "压力窗口必须 > 0")
	}
	if err := validateRates(def.Name, def.Params); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.defs[def.Name][def.Version]; exists {
		return nil, reject(domain.CodeScenarioInvalid, def.Name, "version", "情景 %s@%d 已发布且不可变；修改请使用新版本", def.Name, def.Version)
	}
	// 在锁内做继承解析，保证解析时刻父版本明确。
	if err := s.resolveLocked(def); err != nil {
		return nil, err
	}
	if s.defs[def.Name] == nil {
		s.defs[def.Name] = map[int]domain.ScenarioDef{}
	}
	s.defs[def.Name][def.Version] = def
	if v := s.latest[def.Name]; def.Version > v {
		s.latest[def.Name] = def.Version
	}
	return s.resolved[def.Name][def.Version], nil
}

func (s *Store) resolveLocked(def domain.ScenarioDef) error {
	chain := []ScenarioRef{{Name: def.Name, Version: def.Version}}
	merged := def.Params
	parentName, parentVer := def.Parent, def.ParentVersion
	visited := map[string]bool{key(def.Name, def.Version): true}
	for parentName != "" {
		var (
			parentDef domain.ScenarioDef
			ok        bool
		)
		versions := s.defs[parentName]
		switch {
		case parentVer > 0:
			parentDef, ok = versions[parentVer]
		default:
			v, exists := s.latest[parentName]
			if exists {
				parentDef, ok = versions[v]
				parentVer = v
			}
		}
		if !ok {
			return reject(domain.CodeScenarioNotFound, def.Name, "parent",
				"情景 %s@%d 引用的父情景 %s@%d 未定义", def.Name, def.Version, parentName, parentVer)
		}
		k := key(parentName, parentDef.Version)
		if visited[k] {
			return reject(domain.CodeScenarioInvalid, def.Name, "parent", "情景继承链存在环：%s", k)
		}
		visited[k] = true
		chain = append(chain, ScenarioRef{Name: parentName, Version: parentDef.Version})
		merged = overlayParams(parentDef.Params, merged)
		parentName = parentDef.Parent
		// 若父定义写父时未固定版本，其有效版本在“该父定义发布时刻”已确定，
		// 并保存在其解析结果链里；这里从已发布解析结果复用，避免取到未来新版本。
		if parentDef.ParentVersion > 0 {
			parentVer = parentDef.ParentVersion
		} else if parentDef.Parent != "" {
			if parentResolved, exists := s.resolved[parentDef.Name][parentDef.Version]; exists &&
				len(parentResolved.Chain) > 1 {
				parentVer = parentResolved.Chain[1].Version
			} else {
				parentVer = s.latest[parentDef.Parent]
			}
		}
	}
	r := &Resolved{Name: def.Name, Version: def.Version, Chain: chain, P: merged}
	if s.resolved[def.Name] == nil {
		s.resolved[def.Name] = map[int]*Resolved{}
	}
	s.resolved[def.Name][def.Version] = r
	return nil
}

// overlayParams 把 child 覆盖到 parent 之上：
// 分层 map 按键合并；同一分层键 child 的非 nil 值覆盖 parent；
// 标量类（LiquidateLevel/RunoffProfile）同样按分层键覆盖。
func overlayParams(parent, child domain.ScenarioParams) domain.ScenarioParams {
	out := parent
	mergeRate := func(p, c map[string]*float64) map[string]*float64 {
		m := make(map[string]*float64, len(p)+len(c))
		for k, v := range p {
			m[k] = v
		}
		for k, v := range c {
			m[k] = v
		}
		return m
	}
	mergeStr := func(p, c map[string]string) map[string]string {
		m := make(map[string]string, len(p)+len(c))
		for k, v := range p {
			m[k] = v
		}
		for k, v := range c {
			m[k] = v
		}
		return m
	}
	out.RunoffRate = mergeRate(parent.RunoffRate, child.RunoffRate)
	out.EarlyRedemptionRate = mergeRate(parent.EarlyRedemptionRate, child.EarlyRedemptionRate)
	out.WholesaleRolloverRate = mergeRate(parent.WholesaleRolloverRate, child.WholesaleRolloverRate)
	out.DrawdownRate = mergeRate(parent.DrawdownRate, child.DrawdownRate)
	out.PrepaymentRate = mergeRate(parent.PrepaymentRate, child.PrepaymentRate)
	out.SecurityHaircut = mergeRate(parent.SecurityHaircut, child.SecurityHaircut)
	out.DepositInflowRate = mergeRate(parent.DepositInflowRate, child.DepositInflowRate)
	out.LiquidateLevel = mergeStr(parent.LiquidateLevel, child.LiquidateLevel)
	out.RunoffProfile = mergeStr(parent.RunoffProfile, child.RunoffProfile)
	return out
}

// Get 返回指定版本的已解析情景；version<=0 取最新版本。
func (s *Store) Get(name string, version int) (*Resolved, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions, ok := s.resolved[name]
	if !ok {
		return nil, reject(domain.CodeScenarioNotFound, name, "name", "情景 %q 未发布", name)
	}
	if version <= 0 {
		version = s.latest[name]
	}
	r, ok := versions[version]
	if !ok {
		return nil, reject(domain.CodeScenarioNotFound, name, "version", "情景 %s@%d 不存在", name, version)
	}
	return r, nil
}

// LatestVersion 返回最新已发布版本号。
func (s *Store) LatestVersion(name string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.latest[name]
	if !ok {
		return 0, reject(domain.CodeScenarioNotFound, name, "name", "情景 %q 未发布", name)
	}
	return v, nil
}

// Names 以稳定顺序返回全部情景名，便于审计。
func (s *Store) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.defs))
	for n := range s.defs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func key(name string, version int) string { return fmt.Sprintf("%s@%d", name, version) }

func reject(code domain.Code, id, field, format string, args ...any) error {
	return &domain.RejectError{Code: code, PosID: id, Field: field, Detail: fmt.Sprintf(format, args...)}
}
