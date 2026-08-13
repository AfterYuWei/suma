package database

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func Open(path string) (*gorm.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.AutoMigrate(&User{}, &Session{}, &Setting{}, &Node{}, &DockerTLSCredential{}, &DockerTLSCredentialNode{}, &GitCredentialNode{}, &RegistryCredentialNode{}, &ComposeProject{}, &DeliveryProject{}, &DeliveryProjectNode{}, &DeliveryProjectRegistryCredential{}, &DeliveryTargetState{}, &GitCredential{}, &DeliveryProjectGitCredential{}, &RegistryCredential{}, &DeliveryRelease{}, &DeliveryReleaseDeployment{}, &GitWebhookDelivery{}, &Task{}, &TaskLog{}, &AuditLog{}, &LoginLog{}); err != nil {
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}
	// V1 made Compose names globally unique. V2 scopes names to a node, so the
	// obsolete index must be removed explicitly because AutoMigrate preserves it.
	if err := db.Exec("DROP INDEX IF EXISTS idx_compose_projects_name").Error; err != nil {
		return nil, fmt.Errorf("drop legacy Compose name index: %w", err)
	}
	return db, nil
}
