package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/redact"
	"gorm.io/gorm"
)

const (
	nameLimit   = 120
	subjectLimit = 200
	runLimitCap = 100
)

// ScheduleInput is the create/update payload for a scheduled plan.
type ScheduleInput struct {
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	NodeID   string          `json:"node_id"`
	Subject  string          `json:"subject"`
	Params   json.RawMessage `json:"params"`
	Cron     string          `json:"cron"`
	Timezone string          `json:"timezone"`
	Enabled  *bool           `json:"enabled"`
}

// ScheduleView is a plan plus the outcome of its most recent dispatch.
type ScheduleView struct {
	database.TaskCenterSchedule
	Summary          string     `json:"summary,omitempty"`
	LastTaskID       string     `json:"last_task_id,omitempty"`
	LastTaskStatus   string     `json:"last_task_status,omitempty"`
	LastTaskProgress int        `json:"last_task_progress,omitempty"`
	LastTaskMessage  string     `json:"last_task_message,omitempty"`
	LastTaskFinished *time.Time `json:"last_task_finished_at,omitempty"`
}

// RunView is one dispatch attempt enriched with the task outcome.
type RunView struct {
	database.TaskCenterScheduleRun
	TaskStatus   string     `json:"task_status,omitempty"`
	TaskProgress int        `json:"task_progress,omitempty"`
	FinishedAt   *time.Time `json:"task_finished_at,omitempty"`
}

// Create validates and stores a plan, resolving the node name eagerly so list
// views never depend on the scheduler having run yet.
func (s *Service) Create(ctx context.Context, input ScheduleInput, createdBy string) (database.TaskCenterSchedule, error) {
	row, err := s.build(ctx, ScheduleInput{
		Name: input.Name, Kind: input.Kind, NodeID: strings.TrimSpace(input.NodeID),
		Subject: strings.TrimSpace(input.Subject), Params: input.Params,
		Cron: input.Cron, Timezone: input.Timezone, Enabled: input.Enabled,
	})
	if err != nil {
		return database.TaskCenterSchedule{}, err
	}
	row.CreatedBy = createdBy
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return database.TaskCenterSchedule{}, ErrDuplicate
		}
		return database.TaskCenterSchedule{}, err
	}
	return row, nil
}

// Update changes an existing plan. The kind and subject are immutable; a
// different target means delete and recreate so bindings stay coherent.
func (s *Service) Update(ctx context.Context, id uint, input ScheduleInput, expectedVersion uint64) (database.TaskCenterSchedule, error) {
	var row database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return database.TaskCenterSchedule{}, ErrNotFound
	}
	if expectedVersion != row.Version {
		return database.TaskCenterSchedule{}, ErrVersionConflict
	}
	if len(input.Params) > 0 {
		if spec, ok := s.kinds.Get(row.Kind); ok && spec.ValidateParams != nil {
			if err := spec.ValidateParams(input.Params); err != nil {
				return database.TaskCenterSchedule{}, err
			}
		}
	}
	changes := ScheduleInput{Name: input.Name, Params: input.Params, Cron: input.Cron, Timezone: input.Timezone, Enabled: input.Enabled}
	built := database.TaskCenterSchedule{Name: row.Name, Params: row.Params, Cron: row.Cron, Timezone: row.Timezone, Enabled: row.Enabled}
	if changes.Name != "" {
		built.Name = changes.Name
	}
	if changes.Params != nil {
		built.Params = database.JSONB(changes.Params)
	}
	if changes.Cron != "" {
		built.Cron = changes.Cron
	}
	if changes.Timezone != "" {
		built.Timezone = changes.Timezone
	}
	if changes.Enabled != nil {
		built.Enabled = *changes.Enabled
	}
	next, err := s.nextFor(built.Cron, built.Timezone)
	if err != nil {
		return database.TaskCenterSchedule{}, err
	}
	if !built.Enabled {
		next = nil
	}
	// A typed-nil pointer is skipped by GORM map updates; only an untyped nil
	// writes the SQL NULL that clears the next occurrence.
	var nextValue any
	if built.Enabled {
		nextValue = next
	}
	updates := map[string]any{"name": built.Name, "cron": built.Cron, "timezone": built.Timezone, "enabled": built.Enabled, "params": built.Params, "next_run_at": nextValue, "version": row.Version + 1, "updated_at": s.now()}
	if err := s.db.WithContext(ctx).Model(&database.TaskCenterSchedule{}).Where("id = ? AND version = ?", id, row.Version).Updates(updates).Error; err != nil {
		return database.TaskCenterSchedule{}, err
	}
	// Re-fetch into a fresh struct: GORM leaves populated pointer fields
	// untouched when a reused destination scans a NULL column.
	var refreshed database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).First(&refreshed, "id = ?", id).Error; err != nil {
		return database.TaskCenterSchedule{}, err
	}
	return refreshed, nil
}

// Delete removes a plan and, through the cascade, its dispatch history.
func (s *Service) Delete(ctx context.Context, id uint) error {
	result := s.db.WithContext(ctx).Delete(&database.TaskCenterSchedule{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSchedules drops every plan of one kind bound to a subject; the
// compose removal flow calls it so deleted projects leave no orphans.
func (s *Service) DeleteSchedules(ctx context.Context, kind, nodeID, subject string) error {
	return s.db.WithContext(ctx).Where("kind = ? AND node_id = ? AND subject = ?", kind, nodeID, subject).Delete(&database.TaskCenterSchedule{}).Error
}

// Schedule returns one plan with its latest outcome.
func (s *Service) Schedule(ctx context.Context, id uint) (ScheduleView, error) {
	var row database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return ScheduleView{}, ErrNotFound
	}
	return s.view(ctx, row)
}

// Schedules lists plans, newest first, optionally filtered by kind or node.
func (s *Service) Schedules(ctx context.Context, kind, nodeID string) ([]ScheduleView, error) {
	query := s.db.WithContext(ctx).Model(&database.TaskCenterSchedule{})
	if kind != "" {
		query = query.Where("kind = ?", kind)
	}
	if nodeID != "" {
		query = query.Where("node_id = ?", nodeID)
	}
	var rows []database.TaskCenterSchedule
	if err := query.Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	views := make([]ScheduleView, 0, len(rows))
	for _, row := range rows {
		view, err := s.view(ctx, row)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

// Runs lists the dispatch history of a plan, newest first, with task outcomes.
func (s *Service) Runs(ctx context.Context, scheduleID uint, limit int) ([]RunView, error) {
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, runLimitCap)
	var runs []database.TaskCenterScheduleRun
	if err := s.db.WithContext(ctx).Where("schedule_id = ?", scheduleID).Order("created_at DESC, id DESC").Limit(limit).Find(&runs).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		if run.TaskID != "" {
			ids = append(ids, run.TaskID)
		}
	}
	statuses := map[string]database.Task{}
	if len(ids) > 0 {
		var tasks []database.Task
		if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&tasks).Error; err != nil {
			return nil, err
		}
		for _, row := range tasks {
			statuses[row.ID] = row
		}
	}
	views := make([]RunView, 0, len(runs))
	for _, run := range runs {
		view := RunView{TaskCenterScheduleRun: run}
		if row, ok := statuses[run.TaskID]; ok {
			view.TaskStatus = row.Status
			view.TaskProgress = row.Progress
			view.FinishedAt = row.FinishedAt
		}
		views = append(views, view)
	}
	return views, nil
}

// view enriches a plan row with a params summary and its latest task outcome.
func (s *Service) view(ctx context.Context, row database.TaskCenterSchedule) (ScheduleView, error) {
	view := ScheduleView{TaskCenterSchedule: row}
	if spec, ok := s.kinds.Get(row.Kind); ok && spec.Describe != nil && len(row.Params) > 0 {
		view.Summary = spec.Describe(json.RawMessage(row.Params))
	}
	var run database.TaskCenterScheduleRun
	err := s.db.WithContext(ctx).Where("schedule_id = ? AND task_id <> ''", row.ID).Order("created_at DESC, id DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return view, nil
	}
	if err != nil {
		return ScheduleView{}, err
	}
	view.LastTaskID = run.TaskID
	var row2 database.Task
	if err := s.db.WithContext(ctx).First(&row2, "id = ?", run.TaskID).Error; err == nil {
		view.LastTaskStatus = row2.Status
		view.LastTaskProgress = row2.Progress
		view.LastTaskMessage = row2.Message
		view.LastTaskFinished = row2.FinishedAt
	}
	return view, nil
}

// build validates the input and assembles a storable plan row.
func (s *Service) build(ctx context.Context, input ScheduleInput) (database.TaskCenterSchedule, error) {
	spec, ok := s.kinds.Get(input.Kind)
	if !ok {
		return database.TaskCenterSchedule{}, ErrUnknownKind
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > nameLimit {
		return database.TaskCenterSchedule{}, ErrInvalid
	}
	if len(input.Subject) > subjectLimit || strings.ContainsAny(input.Subject, "\x00\n\r") {
		return database.TaskCenterSchedule{}, ErrInvalid
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	params := input.Params
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	if spec.ValidateParams != nil {
		if err := spec.ValidateParams(params); err != nil {
			return database.TaskCenterSchedule{}, err
		}
	}
	nodeID, nodeName := input.NodeID, ""
	if spec.RequiresNode {
		if nodeID == "" {
			return database.TaskCenterSchedule{}, ErrInvalid
		}
		if s.deps.NodeName != nil {
			var err error
			if nodeName, err = s.deps.NodeName(ctx, nodeID); err != nil {
				return database.TaskCenterSchedule{}, err
			}
		}
	} else {
		nodeID = ""
	}
	next, err := s.nextFor(input.Cron, input.Timezone)
	if err != nil {
		return database.TaskCenterSchedule{}, err
	}
	if !enabled {
		next = nil
	}
	return database.TaskCenterSchedule{
		Name: name, Kind: input.Kind, NodeID: nodeID, NodeName: nodeName,
		Subject: input.Subject, Params: database.JSONB(redact.Bounded(string(params), paramsLimit)),
		Cron: input.Cron, Timezone: input.Timezone, Enabled: enabled, Version: 1, NextRunAt: next,
	}, nil
}

func (s *Service) nextFor(spec, timezone string) (*time.Time, error) {
	next, err := NextRuns(spec, timezone, s.now(), 1)
	if err != nil {
		return nil, ErrInvalid
	}
	if len(next) == 0 {
		return nil, ErrInvalid
	}
	return &next[0], nil
}
