package skill

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry is the process-wide skill catalogue. Skill packages register
// their Executor in init() via Register; the manager pulls metadata to
// build LLM tools / HTTP routes; the edge dispatches incoming
// execute_skill RPCs by looking up the Executor by Key.
//
// Concurrency: registration happens at init() time (single-threaded),
// reads happen at runtime (concurrent). RWMutex protects both phases.
var globalRegistry = &Registry{
	skills: map[string]Executor{},
}

type Registry struct {
	mu     sync.RWMutex
	skills map[string]Executor
}

// Register adds an Executor to the global registry. Panics on
// validation failure or duplicate Key — these are author-time errors
// that should crash the binary loudly, not surface as runtime nil
// dispatches. Returns the registered metadata for convenience (init()
// call sites can use _ = skill.Register(...) for clarity).
func Register(e Executor) Metadata {
	return globalRegistry.Register(e)
}

// Get looks up a skill by Key. Returns (nil, false) if the key is
// unknown — the dispatcher converts that into a 404-style error to
// the RPC caller.
func Get(key string) (Executor, bool) {
	return globalRegistry.Get(key)
}

// All returns the complete list of registered skills, sorted by Key.
// Manager uses this to enumerate skills for the LLM tool registry,
// HTTP /skills listing, and UI dropdown.
func All() []Executor {
	return globalRegistry.All()
}

// AllByClass returns only skills whose class matches one of the
// requested classes. Used by manager to filter LLM tools (only Safe
// auto-callable; Mutating + Dangerous gated through workflow).
func AllByClass(classes ...Class) []Executor {
	return globalRegistry.AllByClass(classes...)
}

// Register implements the global Register on a specific Registry. The
// global Registry calls into this; tests construct their own Registry
// to verify duplicate-key / invalid-metadata behavior in isolation.
func (r *Registry) Register(e Executor) Metadata {
	if e == nil {
		panic("skill: Register called with nil Executor")
	}
	m := e.Metadata()
	if err := m.Validate(); err != nil {
		panic(fmt.Sprintf("skill: %q invalid metadata: %v", m.Key, err))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[m.Key]; exists {
		panic(fmt.Sprintf("skill: duplicate Key %q", m.Key))
	}
	r.skills[m.Key] = e
	return m
}

// Get on the Registry instance.
func (r *Registry) Get(key string) (Executor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.skills[key]
	return e, ok
}

// All on the Registry instance.
func (r *Registry) All() []Executor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Executor, 0, len(r.skills))
	for _, e := range r.skills {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Metadata().Key < out[j].Metadata().Key
	})
	return out
}

// AllByClass on the Registry instance.
func (r *Registry) AllByClass(classes ...Class) []Executor {
	allow := make(map[Class]struct{}, len(classes))
	for _, c := range classes {
		allow[c] = struct{}{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Executor, 0, len(r.skills))
	for _, e := range r.skills {
		if _, ok := allow[e.Metadata().EffectiveClass()]; ok {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Metadata().Key < out[j].Metadata().Key
	})
	return out
}

// ErrNotFound is returned when a skill key is unknown.
var ErrNotFound = errors.New("skill: not found")

// NewRegistryForTest builds a fresh Registry instance — useful for unit
// tests that want isolated state. Production code uses the package-level
// globalRegistry via Register/Get/All.
func NewRegistryForTest() *Registry {
	return &Registry{skills: map[string]Executor{}}
}
