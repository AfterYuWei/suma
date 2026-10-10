package audit

import (
	"context"
	"sync"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

type Entry struct {
	ID           uint   `json:"id"`
	Scope        string `json:"scope"`
	NodeID       string `json:"node_id"`
	NodeName     string `json:"node_name"`
	UserID       *uint  `json:"user_id,omitempty"`
	Action       string `json:"action"`
	ResourceType string `json:"resource_type"`
	ResourceName string `json:"resource_name"`
	IP           string `json:"ip"`
	Result       string `json:"result"`
	TaskID       string `json:"task_id,omitempty"`
	ReleaseID    *uint  `json:"release_id,omitempty"`
	CreatedAt    any    `json:"created_at"`
}
type Service struct {
	db   *gorm.DB
	mu   sync.RWMutex
	sink func(database.AuditLog)
}

func NewService(db *gorm.DB) *Service { return &Service{db: db} }
func (s *Service) Record(ctx context.Context, userID *uint, action, resourceType, resourceName, ip, result string) error {
	return s.RecordControlPlane(ctx, userID, action, resourceType, resourceName, ip, result)
}
func (s *Service) RecordControlPlane(ctx context.Context, userID *uint, action, resourceType, resourceName, ip, result string) error {
	return s.RecordLinkedControlPlane(ctx, userID, action, resourceType, resourceName, ip, result, "", nil)
}
func (s *Service) RecordForNode(ctx context.Context, nodeID, nodeName string, userID *uint, action, resourceType, resourceName, ip, result string) error {
	return s.RecordLinkedForNode(ctx, nodeID, nodeName, userID, action, resourceType, resourceName, ip, result, "", nil)
}
func (s *Service) RecordLinked(ctx context.Context, userID *uint, action, resourceType, resourceName, ip, result, taskID string, releaseID *uint) error {
	return s.RecordLinkedControlPlane(ctx, userID, action, resourceType, resourceName, ip, result, taskID, releaseID)
}
func (s *Service) RecordLinkedControlPlane(ctx context.Context, userID *uint, action, resourceType, resourceName, ip, result, taskID string, releaseID *uint) error {
	return s.record(ctx, database.AuditLog{Scope: task.ScopeControlPlane, UserID: userID, Action: action, ResourceType: resourceType, ResourceName: resourceName, IP: ip, Result: result, TaskID: taskID, ReleaseID: releaseID})
}
func (s *Service) RecordLinkedForNode(ctx context.Context, nodeID, nodeName string, userID *uint, action, resourceType, resourceName, ip, result, taskID string, releaseID *uint) error {
	return s.record(ctx, database.AuditLog{Scope: task.ScopeNode, NodeID: nodeID, NodeName: nodeName, UserID: userID, Action: action, ResourceType: resourceType, ResourceName: resourceName, IP: ip, Result: result, TaskID: taskID, ReleaseID: releaseID})
}
func (s *Service) List(ctx context.Context, limit int) ([]database.AuditLog, error) {
	return s.list(ctx, limit, "", "")
}
func (s *Service) ListForNode(ctx context.Context, limit int, nodeID string) ([]database.AuditLog, error) {
	return s.list(ctx, limit, task.ScopeNode, nodeID)
}
func (s *Service) ListControlPlane(ctx context.Context, limit int) ([]database.AuditLog, error) {
	return s.list(ctx, limit, task.ScopeControlPlane, "")
}
func (s *Service) list(ctx context.Context, limit int, scope, nodeID string) ([]database.AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []database.AuditLog
	query := s.db.WithContext(ctx).Order("created_at DESC, id DESC").Limit(limit)
	if scope != "" {
		query = query.Where("scope = ?", scope)
	}
	if nodeID != "" {
		query = query.Where("node_id = ?", nodeID)
	}
	return rows, query.Find(&rows).Error
}

func (s *Service) SetSink(sink func(database.AuditLog)) { s.mu.Lock(); s.sink = sink; s.mu.Unlock() }
func (s *Service) record(ctx context.Context, row database.AuditLog) error {
	if err := s.RecordTx(ctx, s.db, &row); err != nil {
		return err
	}
	s.mu.RLock()
	sink := s.sink
	s.mu.RUnlock()
	if sink != nil {
		sink(row)
	}
	return nil
}

// RecordTx joins the caller's transaction. Notification sinks must run only
// after commit.
func (s *Service) RecordTx(ctx context.Context, tx *gorm.DB, row *database.AuditLog) error {
	if row.Source == "" {
		row.Source = "site"
	}
	row.ResourceName = redact.Bounded(row.ResourceName, 2048)
	row.Details = redact.Bounded(row.Details, 1024)
	if row.CreatedAt.IsZero() {
		row.CreatedAt = tx.NowFunc().UTC()
	} else {
		row.CreatedAt = row.CreatedAt.UTC()
	}
	return tx.WithContext(ctx).Create(row).Error
}
