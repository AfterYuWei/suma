package audit

import (
	"context"

	"github.com/dockport/dockport/server/internal/database"
	"gorm.io/gorm"
)

type Entry struct {
	ID           uint   `json:"id"`
	NodeID       string `json:"node_id"`
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
type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }
func (s *Service) Record(ctx context.Context, userID *uint, action, resourceType, resourceName, ip, result string) error {
	return s.RecordForNode(ctx, "local", "Local", userID, action, resourceType, resourceName, ip, result)
}
func (s *Service) RecordForNode(ctx context.Context, nodeID, nodeName string, userID *uint, action, resourceType, resourceName, ip, result string) error {
	return s.RecordLinkedForNode(ctx, nodeID, nodeName, userID, action, resourceType, resourceName, ip, result, "", nil)
}
func (s *Service) RecordLinked(ctx context.Context, userID *uint, action, resourceType, resourceName, ip, result, taskID string, releaseID *uint) error {
	return s.RecordLinkedForNode(ctx, "local", "Local", userID, action, resourceType, resourceName, ip, result, taskID, releaseID)
}
func (s *Service) RecordLinkedForNode(ctx context.Context, nodeID, nodeName string, userID *uint, action, resourceType, resourceName, ip, result, taskID string, releaseID *uint) error {
	return s.db.WithContext(ctx).Create(&database.AuditLog{NodeID: nodeID, NodeName: nodeName, UserID: userID, Action: action, ResourceType: resourceType, ResourceName: resourceName, IP: ip, Result: result, TaskID: taskID, ReleaseID: releaseID}).Error
}
func (s *Service) List(ctx context.Context, limit int) ([]database.AuditLog, error) {
	return s.ListForNode(ctx, limit, "")
}
func (s *Service) ListForNode(ctx context.Context, limit int, nodeID string) ([]database.AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []database.AuditLog
	query := s.db.WithContext(ctx).Order("created_at DESC").Limit(limit)
	if nodeID != "" {
		query = query.Where("node_id = ?", nodeID)
	}
	return rows, query.Find(&rows).Error
}
