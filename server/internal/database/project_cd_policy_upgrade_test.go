package database_test

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
)

func TestProjectCDPolicyUpgradePreservesSnapshotsAndAutomation(t *testing.T) {
	dsn := testutil.DSN(t)
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE TABLE schema_migrations (key varchar(128) PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "0009" || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec("INSERT INTO schema_migrations(key) VALUES($1)", entry.Name()); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"INSERT INTO project_cds(id,node_id,project_name,enabled,automation_enabled,revision,repository_json,environment_ciphertext,credential_ciphertext,webhook_id,webhook_secret,candidate_record_id,applied_record_id) VALUES(41,'one','same',true,true,7,'retained-repository',decode('aabb','hex'),decode('ccdd','hex'),'retained-hook',decode('eeff','hex'),51,52),(42,'two','same',true,false,8,'manual-repository',NULL,NULL,'manual-hook',NULL,NULL,NULL)",
		"INSERT INTO project_cd_records(id,project_cd_id,operation,trigger,status,revision,task_id,snapshot_ciphertext) VALUES(51,41,'check','manual','succeeded',7,'retained-check',decode('aabb','hex')),(52,41,'apply','manual','succeeded',7,'retained-apply',decode('ccdd','hex'))",
	} {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := conn.QueryRow(`SELECT jsonb_build_object('config',(SELECT jsonb_agg(to_jsonb(t)-'reconcile_mode'-'auto_rollback' ORDER BY id) FROM project_cds t),'records',(SELECT jsonb_agg(to_jsonb(t)-'source_record_id' ORDER BY id) FROM project_cd_records t))::text`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	for range 2 {
		db, err := database.Open(dsn)
		if err != nil {
			t.Fatal(err)
		}
		pool, _ := db.DB()
		defer pool.Close()
		if snapshot() != before {
			t.Fatal("upgrade changed an existing project, credential, pointer, revision or snapshot")
		}
		var auto, manual database.ProjectCD
		if err := db.First(&auto, 41).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.First(&manual, 42).Error; err != nil {
			t.Fatal(err)
		}
		if auto.ReconcileMode != "auto" || !auto.AutomationEnabled || auto.AutoRollback || manual.ReconcileMode != "manual" || manual.AutomationEnabled || manual.AutoRollback {
			t.Fatal("upgrade changed existing automation semantics or enabled rollback")
		}
		var count int64
		if err := db.Model(&database.SchemaMigration{}).Count(&count).Error; err != nil || count != int64(len(entries)) {
			t.Fatal("migration ledger did not remain idempotent", err)
		}
	}
}
