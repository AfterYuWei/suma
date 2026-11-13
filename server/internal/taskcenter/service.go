package taskcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound          = errors.New("schedule not found")
	ErrBusy              = errors.New("a previous run of this schedule is still active")
	ErrInvalid           = errors.New("invalid schedule")
	ErrVersionConflict   = errors.New("schedule was modified concurrently")
	ErrUnknownKind       = errors.New("unknown task kind")
	ErrDuplicate         = errors.New("an identical schedule already exists")
	ErrInvalidParams     = errors.New("invalid task parameters")
	ErrPaused            = errors.New("automatic triggers are paused")
	ErrUnsupportedAction = errors.New("unsupported task action")
	ErrBusinessOwned     = errors.New("configure this automation through its business settings")
	ErrStopped           = errors.New("task center is shutting down")
)

const (
	lastErrorLimit = 512
	paramsLimit    = 8192
	snapshotLimit  = 16384
	fireInterval   = 15 * time.Second
)

type Dependencies struct {
	NodeName func(context.Context, string) (string, error)
	Now      func() time.Time
	OnError  func(error)
}

type Service struct {
	db      *gorm.DB
	kinds   *Registry
	deps    Dependencies
	mu      sync.Mutex
	started bool
	stopped bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

type DispatchRequest struct {
	ScheduleID   uint
	Kind         string
	NodeID       string
	Params       json.RawMessage
	Reason       string
	SourceID     string
	ScheduledFor *time.Time
	Action       string
	Actor        Actor
}

func New(db *gorm.DB, kinds *Registry, deps Dependencies) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{db: db, kinds: kinds, deps: deps}
}

// Recover never replays a claimed occurrence. CD intervals may perform one
// fresh check of their current configuration after business recovery finishes.
func (s *Service) Recover(ctx context.Context) error {
	if err := s.db.WithContext(ctx).Model(&database.TaskCenterScheduleRun{}).
		Where("status = ?", "dispatching").Updates(map[string]any{"status": "interrupted", "reason_code": "process_restarted", "error": "SUMA stopped before dispatch completed; not replayed"}).Error; err != nil {
		return err
	}
	now := s.now()
	var rows []database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).Where("archived_at IS NULL").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.validateRule(row); err != nil {
			if err := s.park(ctx, row, err.Error()); err != nil {
				return err
			}
			continue
		}
		if row.ScheduleType == "cron" {
			if _, err := s.nextForRule(row); err != nil {
				if err := s.park(ctx, row, err.Error()); err != nil {
					return err
				}
				continue
			}
		}
		if !row.Enabled || !row.TimerEnabled || row.ScheduleType == "none" {
			continue
		}
		if row.NextRunAt == nil {
			if row.LastError == "" {
				if err := s.park(ctx, row, "next execution time is missing; edit the rule to resume"); err != nil {
					return err
				}
			}
			continue
		}
		if !row.NextRunAt.Before(now) {
			continue
		}
		if row.ScheduleType == "interval" {
			if err := s.db.WithContext(ctx).Model(&row).Where("version = ? AND next_run_at = ?", row.Version, row.NextRunAt).Update("next_run_at", now).Error; err != nil {
				return err
			}
			continue
		}
		next, err := s.nextForRule(row)
		if err != nil {
			if err := s.park(ctx, row, err.Error()); err != nil {
				return err
			}
			continue
		}
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			claimed := tx.Model(&database.TaskCenterSchedule{}).Where("id = ? AND version = ? AND next_run_at = ?", row.ID, row.Version, row.NextRunAt).Update("next_run_at", next)
			if claimed.Error != nil || claimed.RowsAffected == 0 {
				return claimed.Error
			}
			if spec, ok := s.kinds.Get(row.Kind); ok && spec.Advance != nil {
				if err := spec.Advance(ctx, tx, row, next); err != nil {
					return err
				}
			}
			return tx.Create(&database.TaskCenterScheduleRun{ScheduleID: &row.ID, TriggeredBy: ReasonSystem, Status: "skipped", ReasonCode: "missed_while_stopped", Error: "Missed while SUMA was stopped; not replayed", CreatedAt: now}).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Recover(ctx); err != nil {
		cancel()
		return err
	}
	s.ctx, s.cancel, s.started = ctx, cancel, true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(fireInterval)
		defer ticker.Stop()
		s.fireDue(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.fireDue(ctx)
			}
		}
	}()
	return nil
}

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
	if err != nil && s.deps.OnError != nil {
		s.deps.OnError(err)
	}
}

// DispatchSubject is used by existing business endpoints after their ordinary
// authentication or webhook verification. No second scheduling loop calls it.
func (s *Service) DispatchSubject(ctx context.Context, kind, nodeID, subject string, request DispatchRequest) (database.TaskCenterScheduleRun, error) {
	var row database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).Where("kind = ? AND node_id = ? AND subject = ? AND archived_at IS NULL", kind, nodeID, subject).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.TaskCenterScheduleRun{}, ErrNotFound
		}
		return database.TaskCenterScheduleRun{}, err
	}
	request.ScheduleID = row.ID
	if request.Reason == ReasonScheduled && request.ScheduledFor == nil {
		request.ScheduledFor = row.NextRunAt
	}
	return s.Dispatch(ctx, request)
}

// ValidateRun is the final asynchronous gate, after task creation but before
// business work begins. Pausing an admitted trigger must still take effect.
func (s *Service) ValidateRun(ctx context.Context, id uint) error {
	if id == 0 {
		return nil
	}
	var run database.TaskCenterScheduleRun
	if err := s.db.WithContext(ctx).First(&run, id).Error; err != nil {
		return err
	}
	if run.ScheduleID == nil {
		return nil
	}
	var row database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).First(&row, *run.ScheduleID).Error; err != nil {
		return err
	}
	var snapshot struct {
		Rule struct {
			Version uint64 `json:"version"`
		} `json:"rule"`
	}
	if json.Unmarshal(run.RuleSnapshot, &snapshot) != nil || snapshot.Rule.Version != row.Version {
		return ErrVersionConflict
	}
	if row.ArchivedAt != nil || (run.TriggeredBy != ReasonManual && (!row.Enabled || (run.TriggeredBy == ReasonScheduled && !row.TimerEnabled) || (run.TriggeredBy == ReasonWebhook && !row.WebhookEnabled))) {
		return ErrPaused
	}
	if spec, ok := s.kinds.Get(row.Kind); ok && spec.ConfigVersion != nil {
		version, err := spec.ConfigVersion(ctx, s.db.WithContext(ctx), row)
		if err != nil {
			return err
		}
		if version != run.ConfigVersion {
			return ErrVersionConflict
		}
	}
	return nil
}

func (s *Service) fireDue(ctx context.Context) {
	s.report(s.RunDue(ctx))
}

// RunDue scans once using the same admission path as the clock loop. It is
// also used by deterministic business integration tests.
func (s *Service) RunDue(ctx context.Context) error {
	var rows []database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).Where("archived_at IS NULL AND enabled AND timer_enabled AND next_run_at <= ?", s.now()).Order("next_run_at, id").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.fireOne(ctx, row, s.now())
	}
	return nil
}
func (s *Service) fireOne(ctx context.Context, row database.TaskCenterSchedule, _ time.Time) {
	if _, ok := s.kinds.Get(row.Kind); !ok {
		s.report(s.park(ctx, row, "task kind is not registered"))
		return
	}
	if _, err := s.nextForRule(row); err != nil {
		s.report(s.park(ctx, row, err.Error()))
		return
	}
	_, err := s.Dispatch(ctx, DispatchRequest{ScheduleID: row.ID, Reason: ReasonScheduled, ScheduledFor: row.NextRunAt})
	if err != nil && !errors.Is(err, ErrBusy) && !errors.Is(err, ErrPaused) && !errors.Is(err, ErrVersionConflict) {
		s.report(err)
	}
}

// Dispatch records admission before any network operation and hands the
// trigger ID to Task.StartDefinition for transactional task/run association.
func (s *Service) Dispatch(ctx context.Context, request DispatchRequest) (database.TaskCenterScheduleRun, error) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return database.TaskCenterScheduleRun{}, ErrStopped
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	if request.Reason == "" {
		request.Reason = ReasonManual
	}
	if !slices.Contains([]string{ReasonManual, ReasonScheduled, ReasonWebhook, ReasonSystem}, request.Reason) || len(request.SourceID) > 256 {
		return database.TaskCenterScheduleRun{}, ErrInvalidParams
	}
	var row database.TaskCenterSchedule
	var spec KindSpec
	var run database.TaskCenterScheduleRun
	var admissionErr error
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if request.ScheduleID != 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ? AND archived_at IS NULL", request.ScheduleID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrNotFound
				}
				return err
			}
			request.Kind, request.NodeID = row.Kind, row.NodeID
			if request.Params == nil || request.Reason != ReasonManual {
				request.Params = json.RawMessage(row.Params)
			}
		}
		var ok bool
		if spec, ok = s.kinds.Get(request.Kind); !ok {
			return ErrUnknownKind
		}
		if request.Action == "" {
			request.Action = spec.Actions[0]
		}
		if !slices.Contains(spec.Actions, request.Action) {
			return ErrUnsupportedAction
		}
		if spec.RequiresNode && request.NodeID == "" {
			return ErrInvalidParams
		}
		if request.Params == nil {
			request.Params = json.RawMessage("{}")
		}
		if len(request.Params) > paramsLimit || !json.Valid(request.Params) {
			return ErrInvalidParams
		}
		if spec.ValidateParams != nil {
			if err := spec.ValidateParams(request.Params); err != nil {
				return err
			}
		}
		if request.Reason == ReasonWebhook && !spec.Webhook {
			return ErrUnsupportedAction
		}
		snapshotValue := map[string]any{"rule": row, "kind": request.Kind, "node_id": request.NodeID, "action": request.Action}
		if row.ID == 0 || !bytes.Equal(row.Params, request.Params) {
			snapshotValue["params"] = request.Params
		}
		snapshot, err := redact.JSON(snapshotValue, snapshotLimit)
		if err != nil {
			return fmt.Errorf("%w: trigger snapshot cannot be saved", ErrInvalidParams)
		}
		run = database.TaskCenterScheduleRun{TriggeredBy: request.Reason, SourceID: request.SourceID, Status: "dispatching", ActorID: request.Actor.ID, RuleSnapshot: snapshot, CreatedAt: s.now()}
		if row.ID != 0 {
			run.ScheduleID = &row.ID
			if request.Reason == ReasonScheduled && row.Enabled && row.TimerEnabled {
				if request.ScheduledFor == nil || row.NextRunAt == nil || !row.NextRunAt.Equal(*request.ScheduledFor) {
					return ErrVersionConflict
				}
				run.ScheduledFor = request.ScheduledFor
				next, err := s.nextForRule(row)
				if err != nil {
					return err
				}
				if err := tx.Model(&row).Updates(map[string]any{"next_run_at": next, "last_run_at": s.now()}).Error; err != nil {
					return err
				}
				if spec.Advance != nil {
					if err := spec.Advance(ctx, tx, row, next); err != nil {
						return err
					}
				}
			}
			if request.Reason != ReasonManual && (!row.Enabled || (request.Reason == ReasonScheduled && !row.TimerEnabled) || (request.Reason == ReasonWebhook && !row.WebhookEnabled)) {
				admissionErr = ErrPaused
				run.Status, run.ReasonCode = "skipped", "automation_paused"
			} else if request.Reason == ReasonScheduled && s.now().Sub(*request.ScheduledFor) >= time.Minute {
				run.Status, run.ReasonCode, run.Error = "skipped", "missed_window", "Missed execution window; not replayed"
			} else {
				busy, err := s.scheduleBusyDB(tx, row.ID)
				if err != nil {
					return err
				}
				if busy {
					admissionErr = ErrBusy
					run.Status, run.ReasonCode = "skipped", "busy"
				}
			}
			if run.Status == "dispatching" && spec.ConfigVersion != nil {
				run.ConfigVersion, admissionErr = spec.ConfigVersion(ctx, tx, row)
				if admissionErr != nil {
					run.Status, run.ReasonCode = "start_failed", "configuration_unavailable"
					updates := map[string]any{"last_error": redact.Bounded(admissionErr.Error(), lastErrorLimit)}
					if invalidBinding(admissionErr) {
						updates["next_run_at"] = nil
						if spec.Advance != nil {
							if err := spec.Advance(ctx, tx, row, nil); err != nil {
								return err
							}
						}
					}
					if err := tx.Model(&database.TaskCenterSchedule{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
						return err
					}
				}
			}
		}
		if admissionErr != nil {
			run.Error = redact.Bounded(admissionErr.Error(), lastErrorLimit)
		}
		return tx.Create(&run).Error
	})
	if err != nil {
		return run, err
	}
	if run.Status != "dispatching" {
		return run, admissionErr
	}
	nodeName := row.NodeName
	var startErr error
	if request.NodeID != "" && s.deps.NodeName != nil {
		nodeName, startErr = s.deps.NodeName(ctx, request.NodeID)
	}
	var taskID string
	if startErr == nil {
		taskID, startErr = spec.Start(ctx, StartRequest{NodeID: request.NodeID, NodeName: nodeName, Params: request.Params, Reason: request.Reason, RunID: run.ID, ScheduleID: run.ScheduleID, Action: request.Action, Actor: request.Actor, ConfigVersion: run.ConfigVersion, Subject: row.Subject, ScheduledFor: request.ScheduledFor})
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if startErr == nil {
		var bound database.TaskCenterScheduleRun
		if err := s.db.WithContext(finishCtx).First(&bound, run.ID).Error; err != nil {
			return run, err
		}
		if bound.TaskID != taskID || bound.Status != "started" {
			startErr = errors.New("task was not atomically linked to its trigger")
		} else {
			run = bound
		}
	}
	if startErr != nil {
		run.Status, run.ReasonCode, run.Error = "start_failed", "start_failed", redact.Bounded(startErr.Error(), lastErrorLimit)
		if err := s.db.WithContext(finishCtx).Model(&database.TaskCenterScheduleRun{}).Where("id = ? AND status = ?", run.ID, "dispatching").Updates(map[string]any{"status": run.Status, "reason_code": run.ReasonCode, "error": run.Error}).Error; err != nil {
			return run, errors.Join(startErr, err)
		}
	}
	if row.ID != 0 {
		if err := s.db.WithContext(finishCtx).Transaction(func(tx *gorm.DB) error {
			updates := map[string]any{"last_error": run.Error}
			if invalidBinding(startErr) {
				updates["next_run_at"] = nil
			}
			updated := tx.Model(&database.TaskCenterSchedule{}).Where("id = ? AND version = ? AND archived_at IS NULL", row.ID, row.Version).Updates(updates)
			if updated.Error != nil || updated.RowsAffected == 0 {
				return updated.Error
			}
			if invalidBinding(startErr) && spec.Advance != nil {
				return spec.Advance(finishCtx, tx, row, nil)
			}
			return nil
		}); err != nil {
			return run, errors.Join(startErr, err)
		}
	}
	return run, startErr
}

func (s *Service) RunNow(ctx context.Context, id uint, options ...DispatchRequest) (database.TaskCenterScheduleRun, error) {
	request := DispatchRequest{}
	if len(options) > 0 {
		request = options[0]
	}
	request.ScheduleID, request.Reason = id, ReasonManual
	return s.Dispatch(ctx, request)
}
func (s *Service) StartManual(ctx context.Context, kind, nodeID string, params json.RawMessage, actors ...Actor) (string, error) {
	request := DispatchRequest{Kind: kind, NodeID: nodeID, Params: params, Reason: ReasonManual}
	if spec, ok := s.kinds.Get(kind); ok && spec.Subject != nil {
		if len(params) > paramsLimit || !json.Valid(params) {
			return "", ErrInvalidParams
		}
		if spec.ValidateParams != nil {
			if err := spec.ValidateParams(params); err != nil {
				return "", err
			}
		}
		var rule database.TaskCenterSchedule
		err := s.db.WithContext(ctx).Where("kind = ? AND node_id = ? AND subject = ? AND archived_at IS NULL", kind, nodeID, spec.Subject(params)).First(&rule).Error
		if err == nil {
			request.ScheduleID = rule.ID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", err
		}
	}
	if len(actors) > 0 {
		request.Actor = actors[0]
	}
	run, err := s.Dispatch(ctx, request)
	return run.TaskID, err
}
func (s *Service) scheduleBusy(ctx context.Context, id uint) (bool, error) {
	return s.scheduleBusyDB(s.db.WithContext(ctx), id)
}
func (s *Service) scheduleBusyDB(db *gorm.DB, id uint) (bool, error) {
	var count int64
	err := db.Model(&database.TaskCenterScheduleRun{}).Where("schedule_id = ? AND (status = 'dispatching' OR (status = 'started' AND task_id IN (SELECT id FROM tasks WHERE status IN ?)))", id, []string{task.StatusPending, task.StatusRunning}).Count(&count).Error
	return count > 0, err
}
func (s *Service) park(ctx context.Context, row database.TaskCenterSchedule, message string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated := tx.Model(&database.TaskCenterSchedule{}).Where("id = ? AND version = ? AND next_run_at IS NOT DISTINCT FROM ? AND archived_at IS NULL", row.ID, row.Version, row.NextRunAt).Updates(map[string]any{"next_run_at": nil, "last_error": redact.Bounded(message, lastErrorLimit)})
		if updated.Error != nil || updated.RowsAffected == 0 {
			return updated.Error
		}
		if spec, ok := s.kinds.Get(row.Kind); ok && spec.Advance != nil {
			return spec.Advance(ctx, tx, row, nil)
		}
		return nil
	})
}

func invalidBinding(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrInvalidParams)
}
