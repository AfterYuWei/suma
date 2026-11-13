package taskcenter_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/cleanup"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/imageupdate"
	"github.com/suma/suma/server/internal/projectcd"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/taskcenter"
	"github.com/suma/suma/server/internal/testutil"
	"gorm.io/gorm"
)

func TestBusinessUpgradeReportPreservesFourKindsAndSecrets(t *testing.T) {
	businessUpgradeFixture(t, nil)
}

func businessUpgradeFixture(t *testing.T, beforeUpgrade func(*testing.T, string, string)) {
	t.Helper()
	dsn := testutil.DSN(t)
	connection, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("open isolated upgrade fixture failed")
	}
	defer connection.Close()
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := connection.Exec(statement, args...); err != nil {
			t.Fatal("upgrade fixture statement failed", err)
		}
	}
	exec("CREATE TABLE schema_migrations (key varchar(128) PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())")
	entries, err := os.ReadDir("../database/migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() > "0011_task_center.sql" || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("../database/migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		exec(string(raw))
		exec("INSERT INTO schema_migrations(key) VALUES($1)", entry.Name())
	}
	root := t.TempDir()
	keyPath := filepath.Join(root, "secret.key")
	store, err := secret.Open(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	keyBefore, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := store.Encrypt("upgrade-fixture-credential")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "compose.yml")
	applicationFile := []byte("services:\n  app:\n    image: alpine:3.24\n")
	if err := os.WriteFile(file, applicationFile, 0600); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"INSERT INTO users(id,username,password_hash) VALUES(41,'retained','retained-hash')",
		"INSERT INTO nodes(id,name,connection_type,endpoint) VALUES('edge','Retained','unix','unix:///unopened.sock'),('paused','Paused','unix','unix:///unopened.sock'),('unauthorized','Unauthorized','unix','unix:///unopened.sock')",
		"INSERT INTO settings(key,value) VALUES('upgrade.retained','keep')",
		"INSERT INTO tasks(id,type,name,status,result) VALUES('finished','image.update','Retained','success','{\"updated\":[\"app\"]}')",
		"INSERT INTO audit_logs(action,result,details) VALUES('retained','success','retained evidence')",
		"INSERT INTO notification_channels(id,config_json,secret_ciphertext) VALUES('channel','retained-config',decode('eeff','hex'))",
		"INSERT INTO notification_deliveries(id,channel_id,status,snapshot_json) VALUES('queued','channel','pending','queued evidence'),('sent','channel','sent','historical evidence')",
		"INSERT INTO task_center_schedules(id,name,kind,node_id,subject,params,cron,timezone,enabled,version) VALUES(7,'Retained','image.update','edge','retained','{\"project\":\"retained\"}','0 4 * * *','UTC',true,9)",
		"SELECT setval(pg_get_serial_sequence('task_center_schedules','id'),7)",
		"INSERT INTO task_center_schedule_runs(id,schedule_id,task_id,error) VALUES(11,7,'finished',''),(12,7,'','original failure'),(13,7,'','')",
		"INSERT INTO task_center_artifacts(task_id,kind,node_id,subject,data) VALUES('finished','image.update','edge','retained','{\"updated\":[\"app\"]}')",
	} {
		exec(statement)
	}
	exec("INSERT INTO git_credentials(id,name,auth_type,secret_ciphertext) VALUES(51,'Retained','http_token',$1)", encrypted)
	exec("INSERT INTO delivery_projects(id,name,deployment_name,git_clone_url,git_credential_id,automation_enabled,sync_interval_seconds,webhook_enabled,webhook_secret,last_sync_at) VALUES(61,'paused-cd','paused-runtime','https://git.example.test/a.git',51,false,35,true,$1,NULL),(62,'enabled-cd','enabled-runtime','https://git.example.test/b.git',51,true,30,false,$1,now())", encrypted)
	exec("INSERT INTO project_cds(id,node_id,project_name,enabled,automation_enabled,sync_interval_seconds,webhook_enabled,webhook_id,credential_ciphertext,environment_ciphertext,failed_fingerprint,last_check_at) VALUES(71,'edge','paused-git',true,false,37,true,'hook-a',$1,$1,'retained-failure',NULL),(72,'edge','enabled-git',true,true,30,false,'hook-b',$1,$1,'',now())", encrypted)
	exec("INSERT INTO project_cd_records(project_cd_id,operation,trigger,status,revision,snapshot_ciphertext) VALUES(71,'check','manual','succeeded',1,$1)", encrypted)
	next := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	for _, nodeID := range []string{"edge", "paused", "unauthorized"} {
		config := cleanup.DefaultConfig("Asia/Shanghai")
		config.Enabled = nodeID != "paused"
		raw, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		var authorizedBy, authorizedAt, nextRun any
		if nodeID == "edge" {
			authorizedBy, authorizedAt, nextRun = 41, time.Now().UTC(), next
		}
		exec("INSERT INTO cleanup_policies(node_id,version,enabled,config_json,authorized_by,authorized_at,next_run_at) VALUES($1,7,$2,$3,$4,$5,$6)", nodeID, config.Enabled, string(raw), authorizedBy, authorizedAt, nextRun)
	}
	tables := []string{"users", "nodes", "git_credentials", "settings", "tasks", "audit_logs", "cleanup_policies", "delivery_projects", "project_cds", "project_cd_records", "notification_channels", "notification_deliveries", "task_center_artifacts"}
	snapshot := func(table string) string {
		t.Helper()
		projection := "to_jsonb(t)"
		if table == "tasks" {
			projection += " - 'parent_task_id' - 'result_error'"
		}
		var result string
		if err := connection.QueryRow("SELECT coalesce(jsonb_agg(value ORDER BY value::text),'[]'::jsonb)::text FROM (SELECT " + projection + " AS value FROM " + table + " t) rows").Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := make(map[string]string, len(tables))
	for _, table := range tables {
		before[table] = snapshot(table)
	}
	if beforeUpgrade != nil {
		beforeUpgrade(t, dsn, root)
	}
	db, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	kinds := taskcenter.NewRegistry()
	center := taskcenter.New(db, kinds, taskcenter.Dependencies{NodeName: func(ctx context.Context, id string) (string, error) {
		var row database.Node
		err := db.WithContext(ctx).First(&row, "id = ?", id).Error
		return row.Name, err
	}})
	tasks := task.NewService(db)
	cleaner := cleanup.NewService(db, tasks, nil, cleanup.Dependencies{})
	delivery := cd.NewService(db, nil, nil, nil, tasks, nil, store)
	project := projectcd.New(db, nil, nil, nil, nil, tasks, nil, store, projectcd.Dependencies{})
	cleaner.SetTaskCenter(center)
	delivery.SetTaskCenter(center)
	project.SetTaskCenter(center)
	for _, spec := range []taskcenter.KindSpec{imageupdate.NewService(db, tasks, imageupdate.Dependencies{}).Kind(), cleaner.AutomationKind(), delivery.AutomationKind(), project.AutomationKind()} {
		if err := kinds.Register(spec); err != nil {
			t.Fatal(err)
		}
	}
	if err := center.BackfillBusinessRules(context.Background(), cleaner.BackfillAutomation, delivery.BackfillAutomation, project.BackfillAutomation); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if snapshot(table) != before[table] {
			t.Errorf("business conversion changed retained %s", table)
		}
	}
	reportSQL, err := os.ReadFile("../../../doc/task-center-upgrade-check.sql")
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.Raw(string(reportSQL)).Row().Scan(&raw); err != nil {
		t.Fatal("read-only migration report failed", err)
	}
	var report struct {
		Complete   bool              `json:"business_conversion_complete"`
		Mismatched int               `json:"missing_or_mismatched"`
		Counts     []json.RawMessage `json:"rule_counts"`
		Parked     []json.RawMessage `json:"parked_rules"`
		Incomplete int               `json:"legacy_incomplete_triggers"`
	}
	if err := json.Unmarshal(raw, &report); err != nil || !report.Complete || report.Mismatched != 0 || len(report.Counts) != 4 || len(report.Parked) != 2 || report.Incomplete != 1 {
		t.Fatalf("unexpected non-secret migration report: %s / %v", raw, err)
	}
	t.Logf("migration verification: %s", raw)
	if err := center.BackfillBusinessRules(context.Background(), func(context.Context, *gorm.DB) error { t.Fatal("business conversion replayed"); return nil }); err != nil {
		t.Fatal(err)
	}
	reopened, err := secret.Open(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	var credential database.GitCredential
	if err := db.First(&credential, 51).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := reopened.Decrypt(credential.SecretCiphertext)
	if err != nil || plaintext != "upgrade-fixture-credential" {
		t.Fatal("retained credential cannot be decrypted with original key")
	}
	keyAfter, err := os.ReadFile(keyPath)
	if err != nil || !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("upgrade replaced application encryption key")
	}
	fileAfter, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(applicationFile, fileAfter) {
		t.Fatal("upgrade changed application files")
	}
}
