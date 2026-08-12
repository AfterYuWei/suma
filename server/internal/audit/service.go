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
	CreatedAt    any    `json:"created_at"`
}
type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }
func (s *Service) Record(ctx context.Context, userID *uint, action, resourceType, resourceName, ip, result string) error {
	return s.db.WithContext(ctx).Create(&database.AuditLog{NodeID: "local", UserID: userID, Action: action, ResourceType: resourceType, ResourceName: resourceName, IP: ip, Result: result}).Error
}
func (s *Service) List(ctx context.Context, limit int) ([]database.AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []database.AuditLog
	return rows, s.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&rows).Error
}
