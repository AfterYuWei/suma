package settings

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata"

	"github.com/suma/suma/server/internal/config"
	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db       *gorm.DB
	defaults map[string]string
	mu       sync.Mutex
	security atomic.Pointer[SecurityPolicy]
}

func NewService(db *gorm.DB, cfg config.Config) *Service {
	return &Service{db: db, defaults: map[string]string{"general.server_name": "SUMA", "general.language": "en", "general.timezone": "system", "docker.compose_command": cfg.ComposeCommand, "storage.compose_root": cfg.ComposeRoot, "storage.data_root": cfg.DataRoot, "storage.backup_root": cfg.BackupRoot, "security.browser_origin": cfg.BrowserOrigin, "security.trusted_proxies": cfg.TrustedProxies, "appearance.theme": "system", "registry.default": ""}}
}

func (s *Service) LoadSecurity(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.Get(ctx)
	if err != nil {
		return err
	}
	policy, err := parseSecurity(values)
	if err != nil {
		return err
	}
	s.security.Store(policy)
	return nil
}

func (s *Service) Security() *SecurityPolicy { return s.security.Load() }
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
	s.mu.Lock()
	defer s.mu.Unlock()
	merged, err := s.Get(ctx)
	if err != nil {
		return nil, err
	}
	for key, value := range values {
		if _, allowed := s.defaults[key]; !allowed {
			return nil, fmt.Errorf("unsupported setting: %s", key)
		}
		merged[key] = value
	}
	zone := strings.TrimSpace(merged["general.timezone"])
	if zone != "system" {
		if zone == "" || zone == "Local" {
			return nil, fmt.Errorf("invalid timezone: select an IANA timezone or system")
		}
		if _, err := time.LoadLocation(zone); err != nil {
			return nil, fmt.Errorf("invalid timezone: select an IANA timezone or system")
		}
	}
	merged["general.timezone"] = zone
	if _, supplied := values["general.timezone"]; supplied {
		values["general.timezone"] = zone
	}
	policy, err := parseSecurity(merged)
	if err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for key, value := range values {
			row := database.Setting{Key: key, Value: value}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.security.Store(policy)
	return merged, nil
}
