package database_test

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"errors"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
	"gorm.io/gorm"
)

func TestDeliveryRecordsUpgradeKeepsAliasesAndHistoricalEvidence(t *testing.T) {
	dsn := testutil.DSN(t)
	connection, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	names := []string{"0001_initial.sql", "0002_container_monitoring.sql", "0003_notification_incidents.sql", "0004_delivery_automation.sql", "0005_delivery_environment.sql"}
	for _, name := range names {
		raw, err := os.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	statements := []string{
		`CREATE TABLE schema_migrations (key varchar(128) PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`,
		`INSERT INTO users(id,username,password_hash) VALUES(41,'retained','retained-password-hash')`,
		`INSERT INTO nodes(id,name,connection_type,endpoint) VALUES('target','Retained','unix','unix:///fixture.sock')`,
		`INSERT INTO git_credentials(id,name,auth_type,secret_ciphertext) VALUES(51,'Retained','http_token',decode('deadbeef','hex'))`,
		`INSERT INTO settings(key,value) VALUES('general.server_name','Retained')`,
		`INSERT INTO delivery_projects(id,name,deployment_name,git_clone_url,reconcile_mode,active_release_id,environment_ciphertext) VALUES(61,'retained','retained','https://example.test/app.git','manual',72,decode('aabb','hex'))`,
		`INSERT INTO delivery_project_nodes(project_id,node_id) VALUES(61,'target')`,
		`INSERT INTO delivery_releases(id,project_id,repository_url,commit_sha,status,trigger_type,task_id,worktree_path,compose_files_json,image_references,config_hash,environment_ciphertext,started_at,finished_at,created_at) VALUES
   (71,61,'https://example.test/app.git','same-commit','succeeded','manual','old-task','/retained/tree','["compose.yml"]','["app:one"]','old-config',decode('ccdd','hex'),'2026-10-01','2026-10-01','2026-10-01'),
   (72,61,'https://example.test/app.git','same-commit','failed','rollback','new-task','/retained/tree','["compose.yml"]','["app:two"]','new-config',decode('eeff','hex'),'2026-10-02','2026-10-02','2026-10-02')`,
		`INSERT INTO delivery_releases(id,project_id,repository_url,commit_sha,status,trigger_type,trigger_actor,approved_by,compose_files_json,image_references) VALUES(73,61,'https://example.test/app.git','another-commit','succeeded','poll','scheduler',41,'["compose.yml"]','[]')`,
		`INSERT INTO delivery_release_deployments(id,release_id,node_id,node_name,status) VALUES(81,71,'target','Retained','succeeded'),(82,72,'target','Retained','failed')`,
		`INSERT INTO delivery_deployment_attempts(id,deployment_id,operation,target_release_id,task_id,status,failure_reason,created_at,finished_at) VALUES(91,81,'deploy',71,'child-old','succeeded','','2026-10-01','2026-10-01'),(92,82,'manual_rollback',72,'child-new','failed','retained failure','2026-10-02','2026-10-02')`,
		`INSERT INTO delivery_target_states(project_id,node_id,active_release_id,observed_commit) VALUES(61,'target',72,'same-commit')`,
		`INSERT INTO audit_logs(id,action,result,release_id,details) VALUES(101,'cd.rollback','failed',72,'retained audit')`,
		`INSERT INTO tasks(id,type,name,status) VALUES('queued','cd.sync','Retained task','pending')`,
		`INSERT INTO notification_channels(id,config_json,secret_ciphertext) VALUES('retained','retained-subscription',decode('ab12','hex'))`,
		`INSERT INTO notification_deliveries(id,channel_id,status,snapshot_json) VALUES('pending','retained','pending','queued-snapshot'),('sent','retained','sent','historical-snapshot')`,
	}
	for _, statement := range statements {
		if _, err := connection.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		if _, err := connection.Exec(`INSERT INTO schema_migrations(key) VALUES($1)`, name); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	var aliases []database.DeliveryRelease
	if err := db.Order("id").Find(&aliases).Error; err != nil || len(aliases) != 3 {
		t.Fatal("lost release IDs", err)
	}
	if aliases[1].CanonicalReleaseID == nil || *aliases[1].CanonicalReleaseID != 71 || aliases[1].ConfigHash != "new-config" || aliases[1].Status != "failed" {
		t.Fatal("lost alias evidence")
	}
	var archivedStatus string
	if err := db.Raw("SELECT legacy_snapshot_json->>'status' FROM delivery_releases WHERE id = ?", 71).Scan(&archivedStatus).Error; err != nil || archivedStatus != "succeeded" {
		t.Fatal("canonical original evidence was not retained", err)
	}
	var records []database.DeliveryRecord
	if err := db.Order("id").Find(&records).Error; err != nil || len(records) != 3 {
		t.Fatal("lost attempt records", err)
	}
	if records[0].ReleaseID != 71 || records[1].ReleaseID != 71 || records[1].FailureReason != "retained failure" || records[1].LegacyAttemptID == nil || *records[1].LegacyAttemptID != 92 || records[0].SnapshotComplete || records[1].SnapshotComplete {
		t.Fatal("fabricated or lost historical evidence")
	}
	if records[2].ActorID != nil || records[2].ActorName != "" || records[2].TriggerType != "legacy" {
		t.Fatal("migration invented an operation actor from approval or sync metadata")
	}
	if string(records[0].EnvironmentCiphertext) != string([]byte{0xcc, 0xdd}) || string(records[1].EnvironmentCiphertext) != string([]byte{0xee, 0xff}) {
		t.Fatal("historical ciphertext changed")
	}
	var attempt database.DeliveryDeploymentAttempt
	if err := db.First(&attempt, 92).Error; err != nil || attempt.RecordID == nil || *attempt.RecordID != records[1].ID || attempt.TargetReleaseID != 72 {
		t.Fatal("attempt link changed", err)
	}
	var state database.DeliveryTargetState
	if err := db.First(&state, "project_id = ?", 61).Error; err != nil || state.ActiveReleaseID == nil || *state.ActiveReleaseID != 71 {
		t.Fatal("active alias not resolved", err)
	}
	var audit database.AuditLog
	if err := db.First(&audit, 101).Error; err != nil || audit.ReleaseID == nil || *audit.ReleaseID != 72 || audit.Details != "retained audit" {
		t.Fatal("audit evidence changed", err)
	}
	var project database.DeliveryProject
	if err := db.First(&project, 61).Error; err != nil || string(project.EnvironmentCiphertext) != string([]byte{0xaa, 0xbb}) {
		t.Fatal("project ciphertext changed", err)
	}
	var credential database.GitCredential
	if err := db.First(&credential, 51).Error; err != nil || string(credential.SecretCiphertext) != string([]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatal("credential changed", err)
	}
	var task database.Task
	if err := db.First(&task, "id = ?", "queued").Error; err != nil || task.Status != "pending" {
		t.Fatal("queued task changed", err)
	}
	var deliveries []database.NotificationDelivery
	if err := db.Order("id").Find(&deliveries).Error; err != nil || len(deliveries) != 2 || deliveries[0].SnapshotJSON != "queued-snapshot" || deliveries[1].SnapshotJSON != "historical-snapshot" {
		t.Fatal("notification evidence changed", err)
	}
	duplicate := database.DeliveryRelease{ProjectID: 61, RepositoryURL: "https://example.test/app.git", CommitSHA: "same-commit", Status: "ready", TriggerType: "manual"}
	if err := db.Create(&duplicate).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatal("Git identity uniqueness is absent", err)
	}
	again, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	againPool, _ := again.DB()
	defer againPool.Close()
	var count int64
	again.Model(&database.DeliveryRecord{}).Count(&count)
	if count != 3 {
		t.Fatal("migration duplicated records")
	}
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	expected := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			expected++
		}
	}
	again.Model(&database.SchemaMigration{}).Count(&count)
	if count != int64(expected) {
		t.Fatal("migration ledger mismatch")
	}
}
