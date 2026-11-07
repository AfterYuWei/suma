// Package taskcenter is the platform module behind the Task Center: a kind
// registry for schedulable task types, the single scheduler that owns every
// timed plan, trigger history and per-run result artifacts. Features plug in
// by registering one KindSpec; they never run their own scheduling loops.
package taskcenter

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

// Reasons a run can be dispatched for.
const (
	ReasonScheduled = "scheduled"
	ReasonManual    = "manual"
)

// StartRequest carries everything a kind needs to launch one run. Params is
// the stored plan or request payload; Reason distinguishes scheduler ticks
// from manual starts.
type StartRequest struct {
	NodeID   string
	NodeName string
	Params   json.RawMessage
	Reason   string
}

// KindSpec registers one schedulable task type with the Task Center. Start
// must launch the underlying task and return its ID; long work always runs
// through the task engine so it is recorded in the Task Center.
type KindSpec struct {
	Type string
	// RequiresNode marks kinds that run against one Docker node; plans and
	// manual starts must then carry a node ID.
	RequiresNode bool
	// ValidateParams checks stored params when a plan is saved or a run starts.
	ValidateParams func(params json.RawMessage) error
	// Describe renders a short human summary of params for list views. May be nil.
	Describe func(params json.RawMessage) string
	// Start launches the underlying task and returns its task ID.
	Start func(ctx context.Context, request StartRequest) (string, error)
}

// KindInfo is the serializable kind description for API consumers.
type KindInfo struct {
	Type         string `json:"type"`
	RequiresNode bool   `json:"requires_node"`
}

// Registry holds the registered kinds. Features register in the application
// wiring before the scheduler starts.
type Registry struct {
	mu    sync.RWMutex
	kinds map[string]KindSpec
}

func NewRegistry() *Registry {
	return &Registry{kinds: map[string]KindSpec{}}
}

// Register adds a kind; a duplicate type is a wiring error.
func (r *Registry) Register(spec KindSpec) error {
	if spec.Type == "" || spec.Start == nil {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.kinds[spec.Type]; exists {
		return ErrDuplicate
	}
	r.kinds[spec.Type] = spec
	return nil
}

func (r *Registry) Get(kind string) (KindSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.kinds[kind]
	return spec, ok
}

// List returns every registered kind sorted by type.
func (r *Registry) List() []KindInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	infos := make([]KindInfo, 0, len(r.kinds))
	for _, spec := range r.kinds {
		infos = append(infos, KindInfo{Type: spec.Type, RequiresNode: spec.RequiresNode})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Type < infos[j].Type })
	return infos
}
