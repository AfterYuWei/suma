package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

var (
	ErrNotFound        = errors.New("schedule not found")
	ErrBusy            = errors.New("a previous run of this schedule is still active")
	ErrInvalid         = errors.New("invalid schedule")
	ErrVersionConflict = errors.New("schedule was modified concurrently")
	ErrUnknownKind     = errors.New("unknown task kind")
	ErrDuplicate       = errors.New("an identical schedule already exists")
	ErrInvalidParams   = errors.New("invalid task parameters")
)

const (
	lastErrorLimit = 512
	paramsLimit     = 8192
	fireInterval   = 15 * time.Second
)

// Dependencies wires the scheduler into its surroundings without import
// cycles. Every field is optional; OnError receives loop errors.
type Dependencies struct {
	// NodeName resolves a node's current display name; an error means the
	// node no longer exists and the run is not started.
	NodeName func(ctx context.Context, nodeID string) (string, error)
	// Now overrides the wall clock in tests.
	Now func() time.Time
	// OnError receives background loop failures.
	OnError func(err error)
}

// Service owns every scheduled plan of the Task Center: the due scan, the
// dispatch history and manual runs. Schedule CRUD lives in schedules.go.
type Service struct {
	db    *gorm.DB
	kinds *Registry
	deps  Dependencies

	mu      sync.Mutex
	started bool
	stopped bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func New(db *gorm.DB, kinds *Registry, deps Dependencies) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{db: db, kinds: kinds, deps: deps}
}

// Recover re-arms plans whose next run fell due while the control plane was
// down. Missed occurrences are skipped on purpose: a restart must never burst
// a backlog of updates or cleanups behind the operator's back.
func (s *Service) Recover(ctx context.Context) error {
	now := s.now()
	var rows []database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).Where("enabled = ? AND next_run_at IS NOT NULL AND next_run_at < ?", true, now).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		next, err := NextRuns(row.Cron, row.Timezone, now, 1)
		if err != nil || len(next) == 0 {
			continue // fireDue reports broken plans; nothing to re-arm from.
		}
		if err := s.db.WithContext(ctx).Model(&database.TaskCenterSchedule{}).Where("id = ? AND next_run_at < ?", row.ID, now).Update("next_run_at", next[0]).Error; err != nil {
			return err
		}
	}
	return nil
}

// Start recovers pending plans and launches the dispatch loop once.
func (s *Service) Start() {
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	s.mu.Unlock()
	if err := s.Recover(s.ctx); err != nil {
		s.report(err)
	}
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(fireInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.fireDue(s.ctx)
			}
		}
	}()
}

// Stop cancels the dispatch loop and waits for it to exit.
func (s *Service) Stop() {
	s.mu.Lock()
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Service) now() time.Time { return s.deps.Now().UTC().Truncate(time.Second) }

func (s *Service) report(err error) {
	if s.deps.OnError != nil {
		s.deps.OnError(err)
	}
}

// fireDue dispatches every plan that fell due. Each plan advances exactly one
// occurrence per tick; a still-active previous run defers the tick.
func (s *Service) fireDue(ctx context.Context) {
	now := s.now()
	var rows []database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).Where("enabled = ? AND next_run_at IS NOT NULL AND next_run_at <= ?", true, now).Order("id").Find(&rows).Error; err != nil {
		s.report(err)
		return
	}
	for _, row := range rows {
		s.fireOne(ctx, row, now)
	}
}

func (s *Service) fireOne(ctx context.Context, row database.TaskCenterSchedule, now time.Time) {
	spec, ok := s.kinds.Get(row.Kind)
	if !ok {
		// The feature behind this kind is gone; park the plan instead of
		// spinning on a due date that can never be honored.
		s.park(ctx, row, "task type "+row.Kind+" is not registered")
		return
	}
	busy, err := s.scheduleBusy(ctx, row.ID)
	if err != nil {
		s.report(err)
		return
	}
	if busy {
		return // previous occurrence still active; the next tick re-checks.
	}
	next, err := NextRuns(row.Cron, row.Timezone, now, 1)
	if err != nil || len(next) == 0 {
		s.park(ctx, row, "invalid schedule: "+errText(err, "cron expression"))
		return
	}
	// Advance the occurrence before starting work so a slow start cannot
	// double-fire the same plan.
	claimed := s.db.WithContext(ctx).Model(&database.TaskCenterSchedule{}).
		Where("id = ? AND next_run_at = ?", row.ID, *row.NextRunAt).
		Updates(map[string]any{"next_run_at": next[0], "last_run_at": now, "version": gorm.Expr("version + 1")})
	if claimed.Error != nil {
		s.report(claimed.Error)
		return
	}
	if claimed.RowsAffected != 1 {
		return // another dispatch already claimed this occurrence.
	}
	nodeID, nodeName := row.NodeID, row.NodeName
	if nodeID != "" && s.deps.NodeName != nil {
		name, err := s.deps.NodeName(ctx, nodeID)
		if err != nil {
			s.recordRun(ctx, row.ID, "", ReasonScheduled, err)
			return
		}
		nodeName = name
	}
	taskID, startErr := spec.Start(ctx, StartRequest{NodeID: nodeID, NodeName: nodeName, Params: json.RawMessage(row.Params), Reason: ReasonScheduled})
	s.recordRun(ctx, row.ID, taskID, ReasonScheduled, startErr)
}

// RunNow starts one manual run of a plan without touching its next occurrence.
func (s *Service) RunNow(ctx context.Context, id uint) (database.TaskCenterScheduleRun, error) {
	var row database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return database.TaskCenterScheduleRun{}, ErrNotFound
	}
	spec, ok := s.kinds.Get(row.Kind)
	if !ok {
		return database.TaskCenterScheduleRun{}, ErrUnknownKind
	}
	busy, err := s.scheduleBusy(ctx, row.ID)
	if err != nil {
		return database.TaskCenterScheduleRun{}, err
	}
	if busy {
		return database.TaskCenterScheduleRun{}, ErrBusy
	}
	nodeID, nodeName := row.NodeID, row.NodeName
	if nodeID != "" && s.deps.NodeName != nil {
		if nodeName, err = s.deps.NodeName(ctx, nodeID); err != nil {
			return database.TaskCenterScheduleRun{}, err
		}
	}
	taskID, startErr := spec.Start(ctx, StartRequest{NodeID: nodeID, NodeName: nodeName, Params: json.RawMessage(row.Params), Reason: ReasonManual})
	return s.recordRun(ctx, row.ID, taskID, ReasonManual, startErr), startErr
}

// StartManual launches a one-off run of a registered kind, not tied to a plan.
func (s *Service) StartManual(ctx context.Context, kind, nodeID string, params json.RawMessage) (string, error) {
	spec, ok := s.kinds.Get(kind)
	if !ok {
		return "", ErrUnknownKind
	}
	if spec.RequiresNode && nodeID == "" {
		return "", ErrInvalidParams
	}
	if spec.ValidateParams != nil {
		if err := spec.ValidateParams(params); err != nil {
			return "", err
		}
	}
	nodeName := ""
	if nodeID != "" && s.deps.NodeName != nil {
		var err error
		if nodeName, err = s.deps.NodeName(ctx, nodeID); err != nil {
			return "", err
		}
	}
	return spec.Start(ctx, StartRequest{NodeID: nodeID, NodeName: nodeName, Params: params, Reason: ReasonManual})
}

// scheduleBusy reports whether the most recent run of a plan is still active.
func (s *Service) scheduleBusy(ctx context.Context, scheduleID uint) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&database.Task{}).Where(
		"status IN ? AND id IN (SELECT task_id FROM task_center_schedule_runs WHERE schedule_id = ? AND task_id <> '' ORDER BY created_at DESC, id DESC LIMIT 1)",
		[]string{task.StatusPending, task.StatusRunning}, scheduleID,
	).Count(&count).Error
	return count > 0, err
}

func (s *Service) recordRun(ctx context.Context, scheduleID uint, taskID, reason string, startErr error) database.TaskCenterScheduleRun {
	run := database.TaskCenterScheduleRun{ScheduleID: scheduleID, TaskID: taskID, TriggeredBy: reason, CreatedAt: s.now()}
	if startErr != nil {
		run.Error = redact.Bounded(startErr.Error(), lastErrorLimit)
	}
	if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
		s.report(err)
	}
	if startErr != nil {
		if err := s.db.WithContext(ctx).Model(&database.TaskCenterSchedule{}).Where("id = ?", scheduleID).Update("last_error", run.Error).Error; err != nil {
			s.report(err)
		}
	} else if err := s.db.WithContext(ctx).Model(&database.TaskCenterSchedule{}).Where("id = ?", scheduleID).Update("last_error", "").Error; err != nil {
		s.report(err)
	}
	return run
}

// park takes a permanently broken plan out of the due scan and records why.
func (s *Service) park(ctx context.Context, row database.TaskCenterSchedule, message string) {
	result := s.db.WithContext(ctx).Model(&database.TaskCenterSchedule{}).
		Where("id = ? AND next_run_at = ?", row.ID, *row.NextRunAt).
		Updates(map[string]any{"next_run_at": nil, "last_error": redact.Bounded(message, lastErrorLimit), "version": gorm.Expr("version + 1")})
	if result.Error != nil {
		s.report(result.Error)
	}
}

func errText(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}
