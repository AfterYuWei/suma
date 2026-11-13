package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	nameLimit    = 120
	subjectLimit = 200
	runLimitCap  = 100
)

type ScheduleInput struct {
	Name            string          `json:"name"`
	Kind            string          `json:"kind"`
	NodeID          string          `json:"node_id"`
	Subject         string          `json:"subject"`
	Params          json.RawMessage `json:"params"`
	Cron            string          `json:"cron"`
	Timezone        string          `json:"timezone"`
	Enabled         *bool           `json:"enabled"`
	ScheduleType    string          `json:"schedule_type"`
	IntervalSeconds int             `json:"interval_seconds"`
	TimerEnabled    *bool           `json:"timer_enabled"`
	WebhookEnabled  bool            `json:"-"`
}
type ScheduleView struct {
	database.TaskCenterSchedule
	Summary          string     `json:"summary,omitempty"`
	EffectiveStatus  string     `json:"effective_status"`
	LastRunID        uint       `json:"last_run_id,omitempty"`
	LastRunStatus    string     `json:"last_run_status,omitempty"`
	LastRunReason    string     `json:"last_run_reason,omitempty"`
	LastRunError     string     `json:"last_run_error,omitempty"`
	LastTaskID       string     `json:"last_task_id,omitempty"`
	LastTaskStatus   string     `json:"last_task_status,omitempty"`
	LastTaskProgress int        `json:"last_task_progress,omitempty"`
	LastTaskMessage  string     `json:"last_task_message,omitempty"`
	LastTaskFinished *time.Time `json:"last_task_finished_at,omitempty"`
}
type RunView struct {
	database.TaskCenterScheduleRun
	Kind         string     `json:"kind,omitempty"`
	NodeID       string     `json:"node_id,omitempty"`
	NodeName     string     `json:"node_name,omitempty"`
	RuleName     string     `json:"rule_name,omitempty"`
	Subject      string     `json:"subject,omitempty"`
	TaskStatus   string     `json:"task_status,omitempty"`
	TaskProgress int        `json:"task_progress,omitempty"`
	FinishedAt   *time.Time `json:"task_finished_at,omitempty"`
}
type ScheduleFilter struct {
	Scope           string
	IncludeArchived bool
}
type RunPage struct {
	Items []RunView `json:"items"`
	Total int64     `json:"total"`
}
type RunFilter struct {
	ScheduleID                    uint
	Scope, NodeID, Source, Status string
	Summary                       bool
}

// LockOwned establishes rule-before-business-row ordering for both editor
// entrypoints, so a business save cannot deadlock a center-side update.
func (s *Service) LockOwned(tx *gorm.DB, kind, nodeID, subject string, expected ...uint64) error {
	var row database.TaskCenterSchedule
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("kind = ? AND node_id = ? AND subject = ? AND archived_at IS NULL", kind, nodeID, subject).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if len(expected) > 0 && expected[0] != 0 {
			return ErrVersionConflict
		}
		return nil
	}
	if err == nil && len(expected) > 0 && row.Version != expected[0] {
		return ErrVersionConflict
	}
	return err
}

func (s *Service) Create(ctx context.Context, input ScheduleInput, createdBy string, actors ...Actor) (database.TaskCenterSchedule, error) {
	spec, ok := s.kinds.Get(input.Kind)
	if !ok {
		return database.TaskCenterSchedule{}, ErrUnknownKind
	}
	if spec.BusinessOwned {
		return database.TaskCenterSchedule{}, ErrBusinessOwned
	}
	row, err := s.build(ctx, input)
	if err != nil {
		return row, err
	}
	row.CreatedBy = createdBy
	if spec.ValidateChange != nil {
		if err := spec.ValidateChange(ctx, row); err != nil {
			return row, err
		}
	}
	actor := Actor{Name: createdBy}
	if len(actors) > 0 {
		actor = actors[0]
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if spec.Change != nil {
			if err := spec.Change(ctx, tx, database.TaskCenterSchedule{}, row, actor); err != nil {
				return err
			}
		}
		return tx.Create(&row).Error
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		err = ErrDuplicate
	}
	return row, err
}

func (s *Service) Update(ctx context.Context, id uint, input ScheduleInput, expectedVersion uint64, actors ...Actor) (database.TaskCenterSchedule, error) {
	actor := Actor{}
	if len(actors) > 0 {
		actor = actors[0]
	}
	var row database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).First(&row, "id = ? AND archived_at IS NULL", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return row, ErrNotFound
		}
		return row, err
	}
	if row.Version != expectedVersion {
		return row, ErrVersionConflict
	}
	if (input.Kind != "" && input.Kind != row.Kind) || (input.NodeID != "" && input.NodeID != row.NodeID) || (input.Subject != "" && input.Subject != row.Subject) {
		return row, ErrInvalid
	}
	old := row
	if input.Name != "" {
		row.Name = strings.TrimSpace(input.Name)
	}
	if input.Params != nil {
		row.Params = database.JSONB(input.Params)
	}
	if input.Cron != "" {
		row.Cron = input.Cron
	}
	if input.Timezone != "" {
		row.Timezone = input.Timezone
	}
	if input.Enabled != nil {
		row.Enabled = *input.Enabled
	}
	if input.TimerEnabled != nil {
		row.TimerEnabled = *input.TimerEnabled
	}
	if input.ScheduleType != "" {
		row.ScheduleType = input.ScheduleType
	}
	if input.IntervalSeconds != 0 {
		row.IntervalSeconds = input.IntervalSeconds
	}
	if row.ScheduleType != "cron" {
		row.Cron = ""
		row.Timezone = "UTC"
	} else {
		row.IntervalSeconds = 0
	}
	if row.ScheduleType == "none" {
		row.IntervalSeconds = 0
	}
	if err := s.validateRule(row); err != nil {
		return row, err
	}
	next, err := s.nextForRule(row)
	if err != nil {
		return row, err
	}
	row.NextRunAt = next
	spec, _ := s.kinds.Get(row.Kind)
	if spec.ValidateChange != nil {
		if err := spec.ValidateChange(ctx, row); err != nil {
			return row, err
		}
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked database.TaskCenterSchedule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ? AND archived_at IS NULL", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if locked.Version != expectedVersion {
			return ErrVersionConflict
		}
		if spec.Change != nil {
			if err := spec.Change(ctx, tx, locked, row, actor); err != nil {
				return err
			}
		}
		row.Version++
		row.LastError = ""
		return s.saveRule(tx, row, old.Version)
	})
	return row, err
}

// PutOwned writes the already validated business configuration's schedule
// projection inside that business service's existing transaction.
func (s *Service) PutOwned(tx *gorm.DB, input ScheduleInput) (database.TaskCenterSchedule, error) {
	var current database.TaskCenterSchedule
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("kind = ? AND node_id = ? AND subject = ? AND archived_at IS NULL", input.Kind, input.NodeID, input.Subject).First(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return current, err
	}
	if current.ID != 0 && input.TimerEnabled == nil {
		input.TimerEnabled = &current.TimerEnabled
	}
	row, err := s.build(tx.Statement.Context, input)
	if err != nil {
		return row, err
	}
	if current.ID == 0 {
		return row, tx.Create(&row).Error
	}
	row.ID, row.CreatedAt, row.CreatedBy, row.LastRunAt = current.ID, current.CreatedAt, current.CreatedBy, current.LastRunAt
	row.Name = current.Name
	row.Version = current.Version + 1
	if current.LastError == "" && (current.NextRunAt != nil || !row.Enabled || !row.TimerEnabled) && current.ScheduleType == row.ScheduleType && current.Cron == row.Cron && current.Timezone == row.Timezone && current.IntervalSeconds == row.IntervalSeconds && current.Enabled == row.Enabled && current.TimerEnabled == row.TimerEnabled {
		row.NextRunAt = current.NextRunAt
	}
	return row, s.saveRule(tx, row, current.Version)
}

func (s *Service) saveRule(tx *gorm.DB, row database.TaskCenterSchedule, expected uint64) error {
	var next any
	if row.NextRunAt != nil {
		next = *row.NextRunAt
	}
	result := tx.Model(&database.TaskCenterSchedule{}).Where("id = ? AND version = ? AND archived_at IS NULL", row.ID, expected).Updates(map[string]any{"name": row.Name, "params": row.Params, "cron": row.Cron, "timezone": row.Timezone, "enabled": row.Enabled, "timer_enabled": row.TimerEnabled, "webhook_enabled": row.WebhookEnabled, "schedule_type": row.ScheduleType, "interval_seconds": row.IntervalSeconds, "next_run_at": next, "last_error": row.LastError, "version": row.Version, "updated_at": s.now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, id uint, expected ...uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row database.TaskCenterSchedule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ? AND archived_at IS NULL", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if len(expected) > 0 && row.Version != expected[0] {
			return ErrVersionConflict
		}
		if spec, ok := s.kinds.Get(row.Kind); ok && spec.CanArchive != nil {
			if err := spec.CanArchive(ctx, tx, row); err != nil {
				return err
			}
		}
		return s.ArchiveOwned(tx, row.Kind, row.NodeID, row.Subject)
	})
}
func (s *Service) ArchiveOwned(tx *gorm.DB, kind, nodeID, subject string) error {
	return tx.Model(&database.TaskCenterSchedule{}).Where("kind = ? AND node_id = ? AND subject = ? AND archived_at IS NULL", kind, nodeID, subject).Updates(map[string]any{"archived_at": s.now(), "enabled": false, "timer_enabled": false, "next_run_at": nil, "version": gorm.Expr("version + 1")}).Error
}
func (s *Service) DeleteSchedules(ctx context.Context, kind, nodeID, subject string) error {
	return s.ArchiveOwned(s.db.WithContext(ctx), kind, nodeID, subject)
}

func (s *Service) Schedule(ctx context.Context, id uint) (ScheduleView, error) {
	var row database.TaskCenterSchedule
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ScheduleView{}, ErrNotFound
		}
		return ScheduleView{}, err
	}
	views, err := s.views(ctx, []database.TaskCenterSchedule{row})
	if err != nil {
		return ScheduleView{}, err
	}
	return views[0], nil
}
func (s *Service) Schedules(ctx context.Context, kind, nodeID string, filters ...ScheduleFilter) ([]ScheduleView, error) {
	filter := ScheduleFilter{}
	if len(filters) > 0 {
		filter = filters[0]
	}
	query := s.db.WithContext(ctx).Model(&database.TaskCenterSchedule{})
	if !filter.IncludeArchived {
		query = query.Where("archived_at IS NULL")
	}
	if kind != "" {
		query = query.Where("kind = ?", kind)
	}
	if nodeID != "" {
		query = query.Where("node_id = ?", nodeID)
	}
	if filter.Scope == "control_plane" {
		query = query.Where("node_id = ''")
	} else if filter.Scope == "node" {
		query = query.Where("node_id <> ''")
	}
	var rows []database.TaskCenterSchedule
	if err := query.Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return s.views(ctx, rows)
}
func (s *Service) views(ctx context.Context, rows []database.TaskCenterSchedule) ([]ScheduleView, error) {
	views := make([]ScheduleView, len(rows))
	if len(rows) == 0 {
		return views, nil
	}
	ids := make([]uint, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	var runs []database.TaskCenterScheduleRun
	if err := s.db.WithContext(ctx).Raw("SELECT DISTINCT ON (schedule_id) id,schedule_id,task_id,triggered_by,status,reason_code,error,created_at FROM task_center_schedule_runs WHERE schedule_id IN ? ORDER BY schedule_id,created_at DESC,id DESC", ids).Scan(&runs).Error; err != nil {
		return nil, err
	}
	latest := map[uint]database.TaskCenterScheduleRun{}
	taskIDs := []string{}
	for _, run := range runs {
		latest[*run.ScheduleID] = run
		if run.TaskID != "" {
			taskIDs = append(taskIDs, run.TaskID)
		}
	}
	tasks := map[string]database.Task{}
	if len(taskIDs) > 0 {
		var records []database.Task
		if err := s.db.WithContext(ctx).Omit("params", "result").Where("id IN ?", taskIDs).Find(&records).Error; err != nil {
			return nil, err
		}
		for _, row := range records {
			tasks[row.ID] = row
		}
	}
	for i, row := range rows {
		view := ScheduleView{TaskCenterSchedule: row, EffectiveStatus: "enabled"}
		if row.ArchivedAt != nil {
			view.EffectiveStatus = "archived"
		} else if !row.Enabled {
			view.EffectiveStatus = "paused"
		} else if _, registered := s.kinds.Get(row.Kind); !registered || (row.ScheduleType != "none" && row.TimerEnabled && row.NextRunAt == nil) {
			view.EffectiveStatus = "parked"
		} else if !row.TimerEnabled {
			view.EffectiveStatus = "timer_paused"
		}
		if spec, ok := s.kinds.Get(row.Kind); ok && spec.Describe != nil {
			view.Summary = spec.Describe(json.RawMessage(row.Params))
		}
		if run, ok := latest[row.ID]; ok {
			view.LastRunID, view.LastRunStatus, view.LastRunReason, view.LastRunError = run.ID, run.Status, run.ReasonCode, run.Error
			view.LastTaskID = run.TaskID
			if record, ok := tasks[run.TaskID]; ok {
				view.LastTaskStatus, view.LastTaskProgress, view.LastTaskMessage, view.LastTaskFinished = record.Status, record.Progress, record.Message, record.FinishedAt
			}
		}
		views[i] = view
	}
	return views, nil
}

func (s *Service) Runs(ctx context.Context, scheduleID uint, limit int) ([]RunView, error) {
	page, err := s.RunsPage(ctx, scheduleID, limit, 0)
	return page.Items, err
}
func (s *Service) RunsPage(ctx context.Context, scheduleID uint, limit, offset int) (RunPage, error) {
	return s.TriggerRecords(ctx, RunFilter{ScheduleID: scheduleID}, limit, offset)
}

func (s *Service) runQuery(ctx context.Context, summary bool) *gorm.DB {
	columns := "r.*"
	if summary {
		columns = "r.id,r.schedule_id,r.task_id,r.triggered_by,r.error,r.status,r.reason_code,r.source_id,r.scheduled_for,r.actor_id,r.config_version,r.created_at"
	}
	return s.db.WithContext(ctx).Model(&database.TaskCenterScheduleRun{}).Table("task_center_schedule_runs AS r").
		Joins("LEFT JOIN task_center_schedules s ON s.id = r.schedule_id").
		Select(columns + `, COALESCE(s.kind,r.rule_snapshot->>'kind','') AS kind,
		 COALESCE(s.node_id,r.rule_snapshot->>'node_id','') AS node_id,
		 COALESCE(s.node_name,r.rule_snapshot->'rule'->>'node_name','') AS node_name,
		 COALESCE(s.name,'') AS rule_name,
		 COALESCE(s.subject,r.rule_snapshot->'params'->>'project','') AS subject`)
}

func (s *Service) TriggerRecord(ctx context.Context, id uint) (RunView, error) {
	var row RunView
	err := s.runQuery(ctx, false).Where("r.id = ?", id).Take(&row).Error
	return row, err
}

// TriggerRecords includes ad-hoc dispatches which have no persistent rule or
// Task. Archived rules remain joined so their original scope is still usable.
func (s *Service) TriggerRecords(ctx context.Context, filter RunFilter, limit, offset int) (RunPage, error) {
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, runLimitCap)
	offset = max(0, offset)
	query := s.runQuery(ctx, filter.Summary)
	if filter.ScheduleID != 0 {
		query = query.Where("r.schedule_id = ?", filter.ScheduleID)
	}
	node := "COALESCE(s.node_id,r.rule_snapshot->>'node_id','')"
	if filter.NodeID != "" {
		query = query.Where(node+" = ?", filter.NodeID)
	}
	if filter.Scope == "node" {
		query = query.Where(node + " <> ''")
	} else if filter.Scope == "control_plane" {
		query = query.Where(node + " = ''")
	}
	if filter.Source != "" {
		query = query.Where("r.triggered_by = ?", filter.Source)
	}
	if filter.Status != "" {
		query = query.Where("r.status = ?", filter.Status)
	}
	page := RunPage{Items: []RunView{}}
	if err := query.Count(&page.Total).Error; err != nil {
		return page, err
	}
	var runs []RunView
	if err := query.Order("r.created_at DESC,r.id DESC").Limit(limit).Offset(offset).Find(&runs).Error; err != nil {
		return page, err
	}
	ids := []string{}
	for _, run := range runs {
		if run.TaskID != "" {
			ids = append(ids, run.TaskID)
		}
	}
	tasks := map[string]database.Task{}
	if len(ids) > 0 {
		var rows []database.Task
		if err := s.db.WithContext(ctx).Omit("params", "result").Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return page, err
		}
		for _, row := range rows {
			tasks[row.ID] = row
		}
	}
	for _, view := range runs {
		if row, ok := tasks[view.TaskID]; ok {
			view.TaskStatus, view.TaskProgress, view.FinishedAt = row.Status, row.Progress, row.FinishedAt
		}
		page.Items = append(page.Items, view)
	}
	return page, nil
}

func (s *Service) build(ctx context.Context, input ScheduleInput) (database.TaskCenterSchedule, error) {
	row := database.TaskCenterSchedule{Name: strings.TrimSpace(input.Name), Kind: input.Kind, NodeID: strings.TrimSpace(input.NodeID), Subject: strings.TrimSpace(input.Subject), Params: database.JSONB(input.Params), Cron: input.Cron, Timezone: input.Timezone, Enabled: true, TimerEnabled: true, Version: 1, ScheduleType: input.ScheduleType, IntervalSeconds: input.IntervalSeconds, WebhookEnabled: input.WebhookEnabled}
	if row.ScheduleType == "" {
		row.ScheduleType = "cron"
	}
	if row.Timezone == "" {
		row.Timezone = "UTC"
	}
	if len(row.Params) == 0 {
		row.Params = database.JSONB("{}")
	}
	if input.Enabled != nil {
		row.Enabled = *input.Enabled
	}
	if input.TimerEnabled != nil {
		row.TimerEnabled = *input.TimerEnabled
	}
	if err := s.validateRule(row); err != nil {
		return row, err
	}
	if row.NodeID != "" && s.deps.NodeName != nil {
		var err error
		row.NodeName, err = s.deps.NodeName(ctx, row.NodeID)
		if err != nil {
			return row, err
		}
	}
	next, err := s.nextForRule(row)
	row.NextRunAt = next
	return row, err
}
func (s *Service) validateRule(row database.TaskCenterSchedule) error {
	spec, ok := s.kinds.Get(row.Kind)
	if !ok {
		return ErrUnknownKind
	}
	if row.Name == "" || len(row.Name) > nameLimit || len(row.Subject) > subjectLimit || strings.ContainsAny(row.Subject, "\x00\n\r") {
		return ErrInvalid
	}
	if spec.RequiresNode != (row.NodeID != "") {
		return ErrInvalid
	}
	if len(row.Cron) > 64 || len(row.Timezone) > 64 || len(row.NodeID) > 64 || (row.WebhookEnabled && !spec.Webhook) {
		return ErrInvalid
	}
	if !slices.Contains(spec.ScheduleTypes, row.ScheduleType) {
		return ErrInvalid
	}
	if len(row.Params) > paramsLimit || !json.Valid(row.Params) {
		return ErrInvalidParams
	}
	if spec.ValidateParams != nil {
		if err := spec.ValidateParams(json.RawMessage(row.Params)); err != nil {
			return err
		}
	}
	switch row.ScheduleType {
	case "cron":
		if row.IntervalSeconds != 0 {
			return ErrInvalid
		}
	case "interval":
		if row.Cron != "" || row.Timezone != "UTC" || row.IntervalSeconds < 30 || row.IntervalSeconds > 86400 {
			return ErrInvalid
		}
	case "none":
		if row.Cron != "" || row.Timezone != "UTC" || row.IntervalSeconds != 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func (s *Service) nextForRule(row database.TaskCenterSchedule) (*time.Time, error) {
	if row.ScheduleType == "none" {
		return nil, nil
	}
	runs, err := s.Preview(ScheduleInput{Kind: row.Kind, ScheduleType: row.ScheduleType, Cron: row.Cron, Timezone: row.Timezone, IntervalSeconds: row.IntervalSeconds}, 1)
	if err != nil || len(runs) == 0 {
		return nil, ErrInvalid
	}
	if !row.Enabled || !row.TimerEnabled {
		return nil, nil
	}
	return &runs[0], nil
}

// Preview uses the registered business clock, including cleanup civil-time
// handling, rather than approximating every schedule as a generic cron.
func (s *Service) Preview(input ScheduleInput, count int) ([]time.Time, error) {
	if input.Kind == "" {
		return NextRuns(input.Cron, input.Timezone, s.now(), count)
	}
	spec, ok := s.kinds.Get(input.Kind)
	if !ok {
		return nil, ErrUnknownKind
	}
	if input.ScheduleType == "" {
		input.ScheduleType = "cron"
	}
	if !slices.Contains(spec.ScheduleTypes, input.ScheduleType) {
		return nil, ErrInvalid
	}
	count = min(max(count, 1), 5)
	if input.ScheduleType == "none" {
		return []time.Time{}, nil
	}
	if input.ScheduleType == "interval" {
		if input.IntervalSeconds < 30 || input.IntervalSeconds > 86400 {
			return nil, ErrInvalid
		}
		runs := make([]time.Time, count)
		for i := range runs {
			runs[i] = s.now().Add(time.Duration(i+1) * time.Duration(input.IntervalSeconds) * time.Second)
		}
		return runs, nil
	}
	if spec.NextRuns != nil {
		return spec.NextRuns(database.TaskCenterSchedule{Kind: input.Kind, ScheduleType: input.ScheduleType, Cron: input.Cron, Timezone: input.Timezone}, s.now(), count)
	}
	return NextRuns(input.Cron, input.Timezone, s.now(), count)
}
