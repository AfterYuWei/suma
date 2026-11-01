package cd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type HistoryQuery struct {
	Source, Operation, Status, Search string
	Page, PageSize                    int
}

// HistoryRecord is a read projection. Each source retains its own record IDs,
// snapshots, task ownership and lifecycle; no Release is created for project CD.
type HistoryRecord struct {
	ID             string     `json:"id"`
	Source         string     `json:"source"`
	RecordID       uint       `json:"record_id"`
	ProjectName    string     `json:"project_name"`
	ProjectID      uint       `json:"project_id"`
	ReleaseID      *uint      `json:"release_id,omitempty"`
	NodeID         string     `json:"node_id,omitempty"`
	NodeName       string     `json:"node_name,omitempty"`
	NodeExists     bool       `json:"node_exists"`
	NodeIDs        []string   `gorm:"-" json:"node_ids"`
	NodeIDsJSON    string     `json:"-"`
	Operation      string     `json:"operation"`
	Status         string     `json:"status"`
	ActorName      string     `json:"actor_name"`
	TaskID         string     `json:"task_id"`
	SourceRecordID *uint      `json:"source_record_id,omitempty"`
	CommitSHA      string     `json:"commit_sha"`
	ConfigHash     string     `json:"config_hash"`
	FailureReason  string     `json:"failure_reason"`
	CreatedAt      time.Time  `json:"created_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type HistoryPage struct {
	Items    []HistoryRecord `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

const historySource = `
SELECT 'continuous_delivery' AS source, r.id AS record_id, r.project_id,
 COALESCE(p.name, '') AS project_name, r.release_id,
 '' AS node_id, '' AS node_name, false AS node_exists, r.node_ids_json,
 r.operation, r.status, r.actor_name, r.task_id, r.source_record_id,
 COALESCE(rel.commit_sha, '') AS commit_sha, r.config_hash, r.failure_reason,
 r.created_at, r.finished_at
FROM delivery_records r
LEFT JOIN delivery_projects p ON p.id = r.project_id
LEFT JOIN delivery_releases rel ON rel.id = r.release_id
UNION ALL
SELECT 'project_cd' AS source, r.id AS record_id, p.id AS project_id,
 p.project_name, NULL::bigint AS release_id,
 p.node_id, COALESCE(NULLIF(task.node_name, ''), node.name, p.node_id) AS node_name,
 node.id IS NOT NULL AS node_exists, '' AS node_ids_json,
 CASE r.operation WHEN 'apply' THEN 'deploy' WHEN 'rollback' THEN 'manual_rollback' ELSE r.operation END AS operation,
 r.status, r.actor AS actor_name, r.task_id, r.source_record_id,
 r.commit_sha, r.config_hash, r.failure_reason, r.created_at, r.finished_at
FROM project_cd_records r
JOIN project_cds p ON p.id = r.project_cd_id
LEFT JOIN nodes node ON node.id = p.node_id
LEFT JOIN tasks task ON task.id = r.task_id AND task.node_id = p.node_id`

func (s *Service) ListHistory(ctx context.Context, q HistoryQuery) (HistoryPage, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 20
	}
	if q.PageSize > 100 {
		q.PageSize = 100
	}
	result := HistoryPage{Items: []HistoryRecord{}, Page: q.Page, PageSize: q.PageSize}
	base := s.db.WithContext(ctx).Table("(" + historySource + ") AS history")
	if q.Source != "" {
		base = base.Where("source = ?", q.Source)
	}
	if q.Operation != "" {
		base = base.Where("operation = ?", q.Operation)
	}
	if q.Status != "" {
		base = base.Where("status = ?", q.Status)
	}
	if search := strings.TrimSpace(q.Search); search != "" {
		pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(search) + "%"
		base = base.Where(`(project_name ILIKE ? OR node_name ILIKE ? OR node_id ILIKE ? OR node_ids_json ILIKE ?
 OR EXISTS (SELECT 1 FROM nodes search_node WHERE search_node.name ILIKE ?
 AND jsonb_exists(COALESCE(NULLIF(history.node_ids_json, ''), '[]')::jsonb, search_node.id)))`, pattern, pattern, pattern, pattern, pattern)
	}
	if err := base.Session(&gorm.Session{}).Count(&result.Total).Error; err != nil {
		return result, err
	}
	if err := base.Order("created_at DESC, source ASC, record_id DESC").Offset((q.Page - 1) * q.PageSize).Limit(q.PageSize).Scan(&result.Items).Error; err != nil {
		return result, err
	}
	for i := range result.Items {
		r := &result.Items[i]
		r.ID = fmt.Sprintf("%s:%d", r.Source, r.RecordID)
		r.NodeIDs = []string{}
		if r.Source == "project_cd" {
			r.NodeIDs = []string{r.NodeID}
		} else if r.NodeIDsJSON != "" {
			if err := json.Unmarshal([]byte(r.NodeIDsJSON), &r.NodeIDs); err != nil {
				return result, fmt.Errorf("decode nodes for delivery record %d: %w", r.RecordID, err)
			}
			if r.NodeIDs == nil {
				r.NodeIDs = []string{}
			}
		}
	}
	return result, nil
}
