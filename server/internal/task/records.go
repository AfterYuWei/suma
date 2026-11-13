package task

import (
	"context"
	"strings"

	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

// RecordView adds the recorded trigger, including a parent's trigger for node
// work within a control-plane operation. Older tasks remain explicitly unknown.
type RecordView struct {
	database.Task
	RunID        uint   `json:"run_id,omitempty"`
	ScheduleID   *uint  `json:"schedule_id,omitempty"`
	ScheduleName string `json:"schedule_name,omitempty"`
	TriggeredBy  string `json:"triggered_by,omitempty"`
}

type RecordQuery struct {
	Scope, NodeID, Status, TypePrefix, Search, Source string
	ScheduleID                                        uint
	Limit, Offset                                     int
	Summary                                           bool
}

// Limit the lookup to one row. Own-trigger precedence must be identical for
// display and filters; the task_id index bounds each metadata lookup.
const recordTriggerJoin = `LEFT JOIN LATERAL (
		SELECT r.id AS run_id, r.schedule_id, r.triggered_by, s.name AS schedule_name
		FROM task_center_schedule_runs r
		LEFT JOIN task_center_schedules s ON s.id = r.schedule_id
		WHERE r.task_id <> '' AND (r.task_id = tasks.id OR
		 (tasks.parent_task_id <> '' AND r.task_id = tasks.parent_task_id))
		ORDER BY (r.task_id = tasks.id) DESC, r.id DESC LIMIT 1
	) trigger_record ON TRUE`

func recordColumns(summary bool) string {
	columns := "tasks.*"
	if summary {
		columns = "tasks.id,tasks.scope,tasks.node_id,tasks.node_name,tasks.type,tasks.name,tasks.status,tasks.progress,tasks.message,tasks.result_error,tasks.parent_task_id,tasks.created_at,tasks.started_at,tasks.finished_at"
	}
	return columns + ", COALESCE(trigger_record.run_id,0) AS run_id, trigger_record.schedule_id, COALESCE(trigger_record.schedule_name,'') AS schedule_name, COALESCE(trigger_record.triggered_by,'') AS triggered_by"
}

func (s *Service) recordQuery(ctx context.Context, summary bool) *gorm.DB {
	return s.db.WithContext(ctx).Model(&database.Task{}).Joins(recordTriggerJoin).Select(recordColumns(summary))
}

func (s *Service) Records(ctx context.Context, input RecordQuery) ([]RecordView, int64, error) {
	query := s.db.WithContext(ctx).Model(&database.Task{})
	if input.Scope != "" {
		query = query.Where("tasks.scope = ?", input.Scope)
	}
	if input.NodeID != "" {
		query = query.Where("tasks.node_id = ?", input.NodeID)
	}
	switch input.Status {
	case "active":
		query = query.Where("tasks.status IN ?", []string{StatusPending, StatusRunning})
	case "finished":
		query = query.Where("tasks.status IN ?", []string{StatusSuccess, StatusFailed, StatusCanceled})
	case "":
	default:
		query = query.Where("tasks.status = ?", input.Status)
	}
	escape := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	if input.TypePrefix != "" {
		query = query.Where("tasks.type LIKE ?", escape.Replace(input.TypePrefix)+"%")
	}
	if input.Search != "" {
		pattern := "%" + escape.Replace(input.Search) + "%"
		query = query.Where("(tasks.name ILIKE ? OR tasks.message ILIKE ? OR tasks.type ILIKE ? OR tasks.node_name ILIKE ?)", pattern, pattern, pattern, pattern)
	}
	needsTrigger := input.Source != "" || input.ScheduleID != 0
	if needsTrigger {
		query = query.Joins(recordTriggerJoin)
	}
	if input.Source == "unrecorded" {
		query = query.Where("trigger_record.run_id IS NULL")
	} else if input.Source != "" {
		query = query.Where("trigger_record.triggered_by = ?", input.Source)
	}
	if input.ScheduleID != 0 {
		query = query.Where("trigger_record.schedule_id = ?", input.ScheduleID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// Without source/rule filtering, the total only needs the tasks table. Do
	// not look up trigger metadata for every historical row just to count it.
	if !needsTrigger {
		query = query.Joins(recordTriggerJoin)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}
	rows := []RecordView{}
	err := query.Select(recordColumns(input.Summary)).Order("tasks.created_at DESC,tasks.id DESC").Limit(min(limit, 200)).Offset(max(0, input.Offset)).Find(&rows).Error
	return rows, total, err
}

func (s *Service) Record(ctx context.Context, id string) (RecordView, error) {
	var row RecordView
	err := s.recordQuery(ctx, false).Where("tasks.id = ?", id).Take(&row).Error
	return row, err
}
