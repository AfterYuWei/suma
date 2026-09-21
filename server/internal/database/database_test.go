package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type legacyUser struct {
	ID           uint   `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex;size:64;not null"`
	PasswordHash string `gorm:"not null"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (legacyUser) TableName() string { return "users" }

func TestOpenMigratesDatabase(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&User{}) || !db.Migrator().HasTable(&Task{}) || !db.Migrator().HasTable(&NodeGroup{}) || !db.Migrator().HasTable(&NodeGroupNode{}) {
		t.Fatal("expected core tables to be migrated")
	}
}

func TestOpenMigratesLegacyUserProfileColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.AutoMigrate(&legacyUser{}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Create(&legacyUser{Username: "admin", PasswordHash: "hash"}).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := legacy.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"nickname", "email", "avatar_data", "avatar_mime", "avatar_updated_at"} {
		if !db.Migrator().HasColumn(&User{}, column) {
			t.Fatalf("missing migrated users.%s", column)
		}
	}
	var user User
	if err := db.First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" || user.Email != "" || user.Nickname != "" {
		t.Fatalf("legacy user changed during migration: %#v", user)
	}
}

func TestOpenBackfillsNodeGroupsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-nodes.db")
	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.AutoMigrate(&Node{}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Create(&Node{ID: "edge", Name: "Edge", ConnectionType: "unix", Endpoint: "unix:///edge.sock", TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := legacy.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var group NodeGroup
	if err := db.Where("is_default = ?", true).First(&group).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&NodeGroupNode{}).Where("group_id = ? AND node_id = ?", group.ID, "edge").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("default membership count = %d, err = %v", count, err)
	}
	if err := db.Delete(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateNodeGroups(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&NodeGroup{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("one-time migration recreated deleted default group: count=%d err=%v", count, err)
	}
}

func TestScopedHistoryMigrationBackfillsDeliveryStateIdempotently(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	project := DeliveryProject{Name: "production", ReconcileMode: "manual"}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&DeliveryProjectNode{ProjectID: project.ID, NodeID: "edge"}).Error; err != nil {
		t.Fatal(err)
	}
	release := DeliveryRelease{ProjectID: project.ID, CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: "succeeded"}
	if err := db.Create(&release).Error; err != nil {
		t.Fatal(err)
	}
	deployment := DeliveryReleaseDeployment{ReleaseID: release.ID, NodeID: "edge", NodeName: "Edge", TaskID: "node-task", Status: "succeeded", HealthSummary: "healthy"}
	if err := db.Create(&deployment).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Task{ID: "cd-parent", Scope: "node", NodeID: "local", NodeName: "Local", Type: "cd.deploy", Name: "deploy", Status: "success"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&AuditLog{Scope: "node", NodeID: "local", NodeName: "Local", Action: "settings.update", Result: "success"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateNodeScopesAndDeliveryHistory(db); err != nil {
		t.Fatal(err)
	}
	if err := migrateNodeScopesAndDeliveryHistory(db); err != nil {
		t.Fatal(err)
	}
	var attemptCount int64
	_ = db.Model(&DeliveryDeploymentAttempt{}).Where("deployment_id = ?", deployment.ID).Count(&attemptCount).Error
	var state DeliveryTargetState
	stateErr := db.Where("project_id = ? AND node_id = ?", project.ID, "edge").First(&state).Error
	var parent Task
	_ = db.First(&parent, "id = ?", "cd-parent").Error
	var audit AuditLog
	_ = db.First(&audit, "action = ?", "settings.update").Error
	var updatedProject DeliveryProject
	_ = db.First(&updatedProject, project.ID).Error
	if attemptCount != 1 || stateErr != nil || state.ActiveReleaseID == nil || *state.ActiveReleaseID != release.ID || parent.Scope != "control_plane" || parent.NodeID != "" || audit.Scope != "control_plane" || audit.NodeID != "" || updatedProject.ActiveCommit != release.CommitSHA {
		t.Fatalf("backfill mismatch: attempts=%d state=%#v parent=%#v audit=%#v project=%#v err=%v", attemptCount, state, parent, audit, updatedProject, stateErr)
	}
}
