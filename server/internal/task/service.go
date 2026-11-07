package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/database"
	domainEvent "github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/redact"
	"gorm.io/gorm"
)

const (
	ScopeControlPlane = "control_plane"
	ScopeNode         = "node"
	StatusPending     = "pending"
	StatusRunning     = "running"
	StatusSuccess     = "success"
	StatusFailed      = "failed"
	StatusCanceled    = "canceled"

	paramsLimit = 8192
	resultLimit = 65536
)

var ErrNotExecuted = errors.New("operation was not executed")

type Event struct {
	Type     string    `json:"type"`
	Status   string    `json:"status,omitempty"`
	Progress int       `json:"progress,omitempty"`
	Message  string    `json:"message,omitempty"`
	Time     time.Time `json:"time"`
}
type Reporter func(progress int, message string)
type Work func(context.Context, Reporter) error
type IdentifiedWork func(context.Context, string, Reporter) error

// RecordedWork is the preferred work signature: it returns a structured
// result that the engine stores on the task row as JSON output.
type RecordedWork func(context.Context, string, Reporter) (any, error)

// Definition describes a task launched with its input parameters recorded.
// A non-empty NodeID selects node scope, an empty one the control plane.
type Definition struct {
	Type     string
	Name     string
	NodeID   string
	NodeName string
	Params   any
}

type recordedWork func(context.Context, string, Reporter) (any, error)

func recorded(work IdentifiedWork) recordedWork {
	return func(ctx context.Context, id string, report Reporter) (any, error) {
		return nil, work(ctx, id, report)
	}
}
type Service struct {
	db          *gorm.DB
	sink        domainEvent.Sink
	mu          sync.RWMutex
	subscribers map[string]map[chan Event]struct{}
	cancels     map[string]context.CancelFunc
	done        map[string]chan struct{}
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, subscribers: map[string]map[chan Event]struct{}{}, cancels: map[string]context.CancelFunc{}, done: map[string]chan struct{}{}}
}

func (s *Service) RecoverInterrupted(ctx context.Context) error {
	now := time.Now()
	return s.db.WithContext(ctx).Model(&database.Task{}).Where("status IN ?", []string{StatusPending, StatusRunning}).Updates(map[string]any{
		"status": StatusCanceled, "progress": 0, "message": "SUMA restarted before the task completed", "finished_at": now,
	}).Error
}

// Start launches a task without recorded parameters. Prefer StartDefinition.
func (s *Service) Start(taskType, name string, work Work) (database.Task, error) {
	if defaultNodeTaskType(taskType) {
		return s.StartForNode("local", "Local", taskType, name, work)
	}
	return s.StartControlPlane(taskType, name, work)
}

// StartControlPlane launches a control-plane task without recorded parameters.
func (s *Service) StartControlPlane(taskType, name string, work Work) (database.Task, error) {
	return s.StartWithIDControlPlane(taskType, name, func(ctx context.Context, _ string, report Reporter) error {
		return work(ctx, report)
	})
}

// StartForNode launches a node task without recorded parameters.
func (s *Service) StartForNode(nodeID, nodeName, taskType, name string, work Work) (database.Task, error) {
	return s.StartWithIDForNode(nodeID, nodeName, taskType, name, func(ctx context.Context, _ string, report Reporter) error {
		return work(ctx, report)
	})
}

// StartWithID launches a task without recorded parameters.
func (s *Service) StartWithID(taskType, name string, work IdentifiedWork) (database.Task, error) {
	if defaultNodeTaskType(taskType) {
		return s.StartWithIDForNode("local", "Local", taskType, name, work)
	}
	return s.StartWithIDControlPlane(taskType, name, work)
}

// StartDefinition launches a task with its input parameters recorded on the
// task row; a structured work result is stored on completion. Task Center
// kinds and new call sites should use this entry point.
func (s *Service) StartDefinition(ctx context.Context, def Definition, work RecordedWork) (database.Task, error) {
	scope := ScopeControlPlane
	if def.NodeID != "" {
		scope = ScopeNode
	}
	params := database.JSONB(nil)
	if def.Params != nil {
		raw, err := json.Marshal(def.Params)
		if err != nil {
			return database.Task{}, fmt.Errorf("encode task parameters: %w", err)
		}
		params = database.JSONB(redact.Bounded(string(raw), paramsLimit))
	}
	return s.startWithID(ctx, scope, def.NodeID, def.NodeName, def.Type, def.Name, params, recordedWork(work))
}

func defaultNodeTaskType(taskType string) bool {
	for _, prefix := range []string{"container.", "image.", "network.", "volume.", "compose.", "project.", "system."} {
		if strings.HasPrefix(taskType, prefix) {
			return true
		}
	}
	return false
}

func (s *Service) StartWithIDControlPlane(taskType, name string, work IdentifiedWork) (database.Task, error) {
	return s.startWithID(context.Background(), ScopeControlPlane, "", "", taskType, name, nil, recorded(work))
}

func (s *Service) StartWithIDForNode(nodeID, nodeName, taskType, name string, work IdentifiedWork) (database.Task, error) {
	return s.startWithID(context.Background(), ScopeNode, nodeID, nodeName, taskType, name, nil, recorded(work))
}

func (s *Service) startWithID(ctx context.Context, scope, nodeID, nodeName, taskType, name string, params database.JSONB, work recordedWork) (database.Task, error) {
	id, err := randomID()
	if err != nil {
		return database.Task{}, err
	}
	row := database.Task{ID: id, Scope: scope, NodeID: nodeID, NodeName: nodeName, Type: taskType, Name: name, Status: StatusPending, Params: params}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return row, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[id] = cancel
	s.done[id] = make(chan struct{})
	s.mu.Unlock()
	go s.run(runCtx, row, work)
	return row, nil
}

func (s *Service) run(ctx context.Context, row database.Task, work recordedWork) {
	defer func() {
		s.mu.Lock()
		if cancel := s.cancels[row.ID]; cancel != nil {
			cancel()
		}
		delete(s.cancels, row.ID)
		if done := s.done[row.ID]; done != nil {
			close(done)
			delete(s.done, row.ID)
		}
		s.mu.Unlock()
	}()
	now := time.Now()
	claimed := s.db.Model(&database.Task{}).Where("id = ? AND status = ?", row.ID, StatusPending).Updates(map[string]any{"status": StatusRunning, "started_at": now})
	if claimed.Error != nil || claimed.RowsAffected != 1 {
		return
	}
	s.publish(row.ID, Event{Type: "status", Status: StatusRunning, Time: now})
	reporter := func(progress int, message string) {
		_ = s.updateProgress(ctx, row.ID, progress, message, true)
	}
	result, workErr := work(ctx, row.ID, reporter)
	status, message := StatusSuccess, "Completed"
	if workErr != nil {
		status, message = StatusFailed, workErr.Error()
	}
	if errors.Is(workErr, ErrNotExecuted) {
		status = StatusCanceled
	}
	if ctx.Err() != nil {
		status, message = StatusCanceled, "Canceled"
	}
	message = redact.Bounded(message, 8192)
	finished := time.Now()
	progress := 100
	if status != StatusSuccess {
		progress = 0
	}
	resultJSON := database.JSONB(nil)
	if result != nil {
		if raw, mErr := json.Marshal(result); mErr == nil {
			resultJSON = database.JSONB(redact.Bounded(string(raw), resultLimit))
		}
	}
	// A caller observing a terminal status must also be able to read its result
	// log. Persist both before publishing completion or emitting domain events.
	persistErr := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&database.TaskLog{TaskID: row.ID, Level: map[bool]string{true: "info", false: "error"}[status == StatusSuccess], Message: message}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Task{}).Where("id = ?", row.ID).Updates(map[string]any{"status": status, "progress": progress, "message": message, "result": resultJSON, "finished_at": finished}).Error
	})
	if persistErr != nil {
		s.publish(row.ID, Event{Type: "status", Status: StatusFailed, Message: "Unable to persist task result; inspect actual state before retrying", Time: finished})
		return
	}
	s.publish(row.ID, Event{Type: "status", Status: status, Progress: progress, Message: message, Time: finished})
	s.mu.Lock()
	sink := s.sink
	s.mu.Unlock()
	if sink != nil {
		severity := "info"
		kind := "task.completed"
		if status == StatusFailed {
			kind = "task.failed"
			severity = "error"
		}
		if status == StatusCanceled {
			kind = "task.canceled"
			severity = "warning"
		}
		sink(domainEvent.Event{Type: kind, Severity: severity, NodeID: row.NodeID, NodeName: row.NodeName, Scope: row.Scope, ResourceType: "task", ResourceID: row.ID, Title: row.Name, Message: message, TaskID: row.ID, Time: finished})
	}
}

// UpdateProgress refreshes a task's live state without appending another
// historical log row. It is intended for replace-in-place output such as
// Docker's per-layer download and extraction counters.
func (s *Service) UpdateProgress(ctx context.Context, id string, progress int, message string) error {
	return s.updateProgress(ctx, id, progress, message, false)
}

func (s *Service) updateProgress(ctx context.Context, id string, progress int, message string, persist bool) error {
	message = redact.Bounded(message, 8192)
	progress = max(0, min(100, progress))
	if err := s.db.WithContext(ctx).Model(&database.Task{}).Where("id = ?", id).Updates(map[string]any{"progress": progress, "message": message}).Error; err != nil {
		return err
	}
	if persist {
		if err := s.db.WithContext(ctx).Create(&database.TaskLog{TaskID: id, Level: "info", Message: message}).Error; err != nil {
			return err
		}
	}
	s.publish(id, Event{Type: "progress", Progress: progress, Message: message, Time: time.Now()})
	return nil
}

func (s *Service) List(ctx context.Context) ([]database.Task, error) {
	return s.list(ctx, "", "")
}
func (s *Service) ListForNode(ctx context.Context, nodeID string) ([]database.Task, error) {
	return s.list(ctx, ScopeNode, nodeID)
}
func (s *Service) ListControlPlane(ctx context.Context) ([]database.Task, error) {
	return s.list(ctx, ScopeControlPlane, "")
}
func (s *Service) list(ctx context.Context, scope, nodeID string) ([]database.Task, error) {
	var rows []database.Task
	query := s.db.WithContext(ctx).Order("created_at DESC").Limit(200)
	if scope != "" {
		query = query.Where("scope = ?", scope)
	}
	if nodeID != "" {
		query = query.Where("node_id = ?", nodeID)
	}
	return rows, query.Find(&rows).Error
}

// ListRecords serves the Task Center records view: filterable, paged history.
// Empty scope means every scope, status filters exactly, typePrefix matches a
// task-type prefix. limit<=0 defaults to 50 (capped at 200); offset paginates.
func (s *Service) ListRecords(ctx context.Context, scope, nodeID, status, typePrefix string, limit, offset int) ([]database.Task, int64, error) {
	query := s.db.WithContext(ctx).Model(&database.Task{})
	if scope != "" {
		query = query.Where("scope = ?", scope)
	}
	if nodeID != "" {
		query = query.Where("node_id = ?", nodeID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if typePrefix != "" {
		query = query.Where("type LIKE ?", typePrefix+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	if offset < 0 {
		offset = 0
	}
	var rows []database.Task
	err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

func (s *Service) Get(ctx context.Context, id string) (database.Task, error) {
	var row database.Task
	return row, s.db.WithContext(ctx).First(&row, "id = ?", id).Error
}

func (s *Service) GetForNode(ctx context.Context, nodeID, id string) (database.Task, error) {
	var row database.Task
	return row, s.db.WithContext(ctx).Where("scope = ? AND node_id = ? AND id = ?", ScopeNode, nodeID, id).First(&row).Error
}
func (s *Service) Logs(ctx context.Context, id string) ([]database.TaskLog, error) {
	var rows []database.TaskLog
	return rows, s.db.WithContext(ctx).Where("task_id = ?", id).Order("created_at ASC").Find(&rows).Error
}
func (s *Service) ReportStep(ctx context.Context, taskID, stepID, status string, current, total int64, progress int) {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	row := database.TaskStep{TaskID: taskID, StepID: stepID}
	s.db.WithContext(ctx).Where("task_id = ? AND step_id = ?", taskID, stepID).Assign(map[string]any{
		"status": status, "current": current, "total": total, "progress": progress,
	}).FirstOrCreate(&row)
}
func (s *Service) Steps(ctx context.Context, id string) ([]database.TaskStep, error) {
	var rows []database.TaskStep
	return rows, s.db.WithContext(ctx).Where("task_id = ?", id).Order("created_at ASC").Find(&rows).Error
}

func (s *Service) LogsForNode(ctx context.Context, nodeID, id string) ([]database.TaskLog, error) {
	if _, err := s.GetForNode(ctx, nodeID, id); err != nil {
		return nil, err
	}
	return s.Logs(ctx, id)
}

func (s *Service) StepsForNode(ctx context.Context, nodeID, id string) ([]database.TaskStep, error) {
	if _, err := s.GetForNode(ctx, nodeID, id); err != nil {
		return nil, err
	}
	return s.Steps(ctx, id)
}
func (s *Service) Cancel(id string) bool {
	s.mu.RLock()
	cancel, ok := s.cancels[id]
	s.mu.RUnlock()
	if ok {
		cancel()
	}
	return ok
}

func (s *Service) CancelForNode(ctx context.Context, nodeID, id string) (bool, error) {
	if _, err := s.GetForNode(ctx, nodeID, id); err != nil {
		return false, err
	}
	return s.Cancel(id), nil
}
func (s *Service) Subscribe(id string) (<-chan Event, func()) {
	channel := make(chan Event, 32)
	s.mu.Lock()
	if s.subscribers[id] == nil {
		s.subscribers[id] = map[chan Event]struct{}{}
	}
	s.subscribers[id][channel] = struct{}{}
	s.mu.Unlock()
	return channel, func() { s.mu.Lock(); delete(s.subscribers[id], channel); close(channel); s.mu.Unlock() }
}
func (s *Service) publish(id string, event Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for channel := range s.subscribers[id] {
		select {
		case channel <- event:
		default:
		}
	}
}
func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate task ID: %w", err)
	}
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

// Wait joins an already launched Task, including its final result transaction.
// It never starts or replays work and returns immediately for finished Tasks.
func (s *Service) Wait(ctx context.Context, id string) error {
	s.mu.RLock()
	done := s.done[id]
	s.mu.RUnlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) SetEventSink(sink domainEvent.Sink) { s.mu.Lock(); s.sink = sink; s.mu.Unlock() }
