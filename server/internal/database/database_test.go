package database_test

import (
	"context"
	"errors"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestPostgresBaselineAndReopen(t *testing.T) {
	dsn := testutil.DSN(t)
	db, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p, _ := db.DB(); _ = p.Close() }()
	for _, model := range database.Models() {
		if !db.Migrator().HasTable(model) {
			t.Errorf("missing table for %T", model)
		}
	}
	for _, name := range []string{"ai_runs", "ai_operations", "ai_audits", "ai_conversations", "ai_messages", "a_iplan_steps", "ai_plan_steps", "ai_interactions", "ai_checkpoints", "ai_tool_calls", "ai_workflow_events", "ai_compose_drafts", "notification_bindings", "notification_actions", "notification_chat_streams"} {
		if db.Migrator().HasTable(name) {
			t.Errorf("removed feature table still exists: %s", name)
		}
	}
	for _, column := range []string{"run_id", "operation_id", "binding_id", "external_user_id", "chat_id"} {
		if db.Migrator().HasColumn(&database.AuditLog{}, column) {
			t.Errorf("removed audit column still exists: %s", column)
		}
	}
	if !db.Migrator().HasColumn(&database.NotificationDelivery{}, "ChatID") {
		t.Fatal("delivery recipient column is absent from fresh schema")
	}
	row := database.User{Username: "retained", Email: "User@Example.test", PasswordHash: "fixture"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	channel := database.NotificationChannel{ID: "retained-channel", Name: "Notification", Provider: "telegram", ConfigJSON: `{"auto_discover":true}`, SecretCiphertext: []byte("encrypted-fixture"), Version: 1}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p, _ := reopened.DB(); _ = p.Close() }()
	var found database.User
	if err := reopened.First(&found, row.ID).Error; err != nil || found.Username != row.Username {
		t.Fatal("reopen lost PostgreSQL state")
	}
	var retained database.NotificationChannel
	if err := reopened.First(&retained, "id = ?", channel.ID).Error; err != nil || retained.ConfigJSON != channel.ConfigJSON || string(retained.SecretCiphertext) != string(channel.SecretCiphertext) {
		t.Fatal("reopen lost notification configuration", err)
	}
	var groups int64
	reopened.Model(&database.NodeGroup{}).Count(&groups)
	if groups != 1 {
		t.Fatal("default group duplicated")
	}
	if err := reopened.Create(&database.User{Username: "other", Email: "user@example.test", PasswordHash: "fixture"}).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatal("case-insensitive email uniqueness was not enforced")
	}
	if err := reopened.Create(&database.NodeGroup{Name: "default"}).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatal("case-insensitive group uniqueness was not enforced")
	}
}

func TestPostgresRollbackAndTimeByteFields(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("rollback")
	if err := db.Transaction(func(tx *gorm.DB) error {
		returnErr := tx.Create(&database.Setting{Key: "rollback", Value: "fixture"}).Error
		if returnErr != nil {
			return returnErr
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	var count int64
	db.Model(&database.Setting{}).Where("key = ?", "rollback").Count(&count)
	if count != 0 {
		t.Fatal("transaction did not roll back")
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	row := database.FileRevision{NodeID: "node", ContainerID: "container", Path: "/test", Ciphertext: []byte{0, 255, 1}, CreatedAt: at}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	var found database.FileRevision
	if err := db.WithContext(context.Background()).Where("created_at >= ?", at).First(&found).Error; err != nil || !found.CreatedAt.Equal(at) || string(found.Ciphertext) != string(row.Ciphertext) {
		t.Fatal("PostgreSQL byte/time round trip failed")
	}
}

func TestPostgresConfigurationErrorsArePrivate(t *testing.T) {
	for _, dsn := range []string{"", "postgres://user:private-password@%invalid/db"} {
		_, err := database.Open(dsn)
		if err == nil || strings.Contains(err.Error(), "private-password") {
			t.Fatal("configuration error missing or exposes credentials")
		}
	}
}
