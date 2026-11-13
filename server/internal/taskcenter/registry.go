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
	"time"

	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

// Reasons a run can be dispatched for.
const (
	ReasonScheduled = "scheduled"
	ReasonManual    = "manual"
	ReasonWebhook   = "webhook"
	ReasonSystem    = "system"
)

type Actor struct {
	ID               *uint
	Name             string
	IP               string
	ConfirmationName string
	Authorize        bool
}

// StartRequest carries everything a kind needs to launch one run. Params is
// the stored plan or request payload; Reason distinguishes scheduler ticks
// from manual starts.
type StartRequest struct {
	NodeID        string
	NodeName      string
	Params        json.RawMessage
	Reason        string
	RunID         uint
	ScheduleID    *uint
	Action        string
	Actor         Actor
	ConfigVersion string
	Subject       string
	ScheduledFor  *time.Time
}

// KindSpec registers one schedulable task type with the Task Center. Start
// must launch the underlying task and return its ID; long work always runs
// through the task engine so it is recorded in the Task Center.
type KindSpec struct {
	Type string
	// RequiresNode marks kinds that run against one Docker node; plans and
	// manual starts must then carry a node ID.
	RequiresNode  bool
	ScheduleTypes []string
	Actions       []string
	Webhook       bool
	BusinessOwned bool
	// ValidateChange performs file/runtime preflight before the short rule
	// transaction. Change below owns transactional business projections only.
	ValidateChange func(context.Context, database.TaskCenterSchedule) error
	// Change validates a rule mutation and writes its business projections in
	// the same transaction. No network calls belong in this callback.
	Change        func(context.Context, *gorm.DB, database.TaskCenterSchedule, database.TaskCenterSchedule, Actor) error
	CanArchive    func(context.Context, *gorm.DB, database.TaskCenterSchedule) error
	ConfigVersion func(context.Context, *gorm.DB, database.TaskCenterSchedule) (string, error)
	NextRuns      func(database.TaskCenterSchedule, time.Time, int) ([]time.Time, error)
	Advance       func(context.Context, *gorm.DB, database.TaskCenterSchedule, *time.Time) error
	// ValidateParams checks stored params when a plan is saved or a run starts.
	ValidateParams func(params json.RawMessage) error
	// Subject resolves the validated business target for manual requests, so
	// project-side checks retain their existing rule's trigger history.
	Subject func(params json.RawMessage) string
	// Describe renders a short human summary of params for list views. May be nil.
	Describe func(params json.RawMessage) string
	// Start launches the underlying task and returns its task ID.
	Start func(ctx context.Context, request StartRequest) (string, error)
}

// KindInfo is the serializable kind description for API consumers.
type KindInfo struct {
	Type          string   `json:"type"`
	RequiresNode  bool     `json:"requires_node"`
	ScheduleTypes []string `json:"schedule_types"`
	Actions       []string `json:"actions"`
	Webhook       bool     `json:"webhook"`
	BusinessOwned bool     `json:"business_owned"`
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
	if len(spec.ScheduleTypes) == 0 {
		spec.ScheduleTypes = []string{"cron"}
	}
	if len(spec.Actions) == 0 {
		spec.Actions = []string{"run"}
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
		infos = append(infos, KindInfo{Type: spec.Type, RequiresNode: spec.RequiresNode, ScheduleTypes: spec.ScheduleTypes, Actions: spec.Actions, Webhook: spec.Webhook, BusinessOwned: spec.BusinessOwned})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Type < infos[j].Type })
	return infos
}
