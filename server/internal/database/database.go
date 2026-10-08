package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/stdlib"
	"io"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open initializes PostgreSQL. Errors never include a DSN or query parameters.
func Open(dsn string) (*gorm.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("SUMA_DATABASE_DSN is required")
	}
	connection, parseErr := pgx.ParseConfig(dsn)
	if parseErr != nil {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}
	if connection.ConnectTimeout == 0 {
		connection.ConnectTimeout = 10 * time.Second
	}
	connectorPool := sql.OpenDB(stdlib.GetConnector(*connection))
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, Conn: connectorPool}), &gorm.Config{TranslateError: true, Logger: logger.New(log.New(io.Discard, "", 0), logger.Config{LogLevel: logger.Silent, ParameterizedQueries: true})})
	if err != nil {
		_ = connectorPool.Close()
		return nil, errors.New("connect to PostgreSQL failed; check database address, TLS and credentials")
	}
	pool, err := db.DB()
	if err != nil {
		return nil, errors.New("initialize PostgreSQL connection pool failed")
	}
	pool.SetMaxOpenConns(20)
	pool.SetMaxIdleConns(5)
	pool.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = migrate(ctx, db); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("initialize PostgreSQL schema: %w", err)
	}
	return db, nil
}

func migrate(ctx context.Context, db *gorm.DB) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return errors.New("read database migrations failed")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Independent test schemas have independent locks.
		if tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_database() || ':' || current_schema(), 0))").Error != nil {
			return errors.New("acquire schema migration lock failed")
		}
		if tx.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (key varchar(128) PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())").Error != nil {
			return errors.New("initialize migration ledger failed")
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			var count int64
			if tx.Model(&SchemaMigration{}).Where("key = ?", entry.Name()).Count(&count).Error != nil {
				return errors.New("read schema migration ledger failed")
			}
			if count > 0 {
				continue
			}
			raw, err := migrations.ReadFile("migrations/" + entry.Name())
			if err != nil {
				return fmt.Errorf("read migration %s failed", entry.Name())
			}
			if tx.Exec(string(raw)).Error != nil {
				return fmt.Errorf("apply migration %s failed", entry.Name())
			}
			if tx.Create(&SchemaMigration{Key: entry.Name(), AppliedAt: time.Now().UTC()}).Error != nil {
				return errors.New("record schema migration failed")
			}
		}
		return nil
	})
}

// Models is used only by schema generation and contract tests, not startup.
func Models() []any {
	return []any{&User{}, &Session{}, &LoginChallenge{}, &TwoFactorEnrollment{}, &TwoFactorRecoveryCode{}, &PasskeyCredential{}, &WebAuthnCeremony{}, &Setting{}, &ImageUpdatePolicy{}, &ImageUpdateRegistryCredential{}, &CleanupPolicy{}, &CleanupRun{}, &Node{}, &AgentEnrollment{}, &AgentCredential{}, &NodeGroup{}, &NodeGroupNode{}, &DockerTLSCredential{}, &DockerTLSCredentialNode{}, &GitCredentialNode{}, &RegistryCredentialNode{}, &DeliveryProject{}, &DeliveryProjectNode{}, &DeliveryProjectRegistryCredential{}, &DeliveryTargetState{}, &GitCredential{}, &DeliveryProjectGitCredential{}, &RegistryCredential{}, &DeliveryRelease{}, &DeliveryReleaseDeployment{}, &DeliveryDeploymentAttempt{}, &GitWebhookDelivery{}, &Task{}, &TaskLog{}, &TaskStep{}, &AuditLog{}, &FileRevision{}, &LoginLog{}, &NotificationChannel{}, &NotificationRule{}, &NotificationEvent{}, &NotificationRead{}, &NotificationDelivery{}, &NotificationBinding{}, &NotificationIncoming{}, &NotificationChat{}, &NotificationAction{}, &NotificationChatStream{}, &AIRun{}, &AIOperation{}, &AIConversation{}, &AIMessage{}, &AIPlanStep{}, &AIInteraction{}, &AICheckpoint{}, &AIToolCall{}, &AIWorkflowEvent{}, &AIComposeDraft{}}
}

func ConnectionDSN(db *gorm.DB) string { return db.Dialector.(*postgres.Dialector).DSN }
