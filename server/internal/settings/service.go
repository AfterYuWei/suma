package settings

import (
	"context"
	"fmt"
	"strings"

	"github.com/dockport/dockport/server/internal/config"
	"github.com/dockport/dockport/server/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db       *gorm.DB
	defaults map[string]string
}

func NewService(db *gorm.DB, cfg config.Config) *Service {
	return &Service{db: db, defaults: map[string]string{"general.server_name": "DockPort", "general.language": "en", "general.timezone": "UTC", "docker.compose_command": cfg.ComposeCommand, "storage.compose_root": cfg.ComposeRoot, "storage.data_root": strings.TrimSuffix(cfg.DatabasePath, "/dockport.db"), "storage.backup_root": cfg.BackupRoot, "security.cookie_secure": fmt.Sprint(cfg.CookieSecure), "appearance.theme": "system", "registry.default": ""}}
}
func (s *Service) Get(ctx context.Context) (map[string]string, error) {
	result := make(map[string]string, len(s.defaults))
	for key, value := range s.defaults {
		result[key] = value
	}
	var rows []database.Setting
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if _, allowed := s.defaults[row.Key]; allowed {
			result[row.Key] = row.Value
		}
	}
	return result, nil
}
func (s *Service) Update(ctx context.Context, values map[string]string) (map[string]string, error) {
	for key, value := range values {
		if _, allowed := s.defaults[key]; !allowed {
			return nil, fmt.Errorf("unsupported setting: %s", key)
		}
		row := database.Setting{Key: key, Value: value}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&row).Error; err != nil {
			return nil, err
		}
	}
	return s.Get(ctx)
}
