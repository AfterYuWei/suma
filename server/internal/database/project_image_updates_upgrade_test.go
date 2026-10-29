package database_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
)

func TestProjectImageUpdateUpgradeRetainsExistingData(t *testing.T) {
	dsn := testutil.DSN(t)
	connection, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, name := range []string{"0001_initial.sql", "0002_container_monitoring.sql", "0003_notification_incidents.sql", "0004_delivery_automation.sql", "0005_delivery_environment.sql"} {
		raw, err := os.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"INSERT INTO users(id,username,password_hash) VALUES(41,'retained','password-hash')",
		"INSERT INTO nodes(id,name,connection_type,endpoint) VALUES('edge','Retained','unix','unix:///fixture.sock')",
		"INSERT INTO settings(key,value) VALUES('general.timezone','Asia/Shanghai')",
		"INSERT INTO registry_credentials(id,name,server_address,auth_type,secret_ciphertext) VALUES(51,'Retained','registry.example.test','basic',decode('aabb','hex'))",
		"INSERT INTO image_update_policies(node_id,version,enabled,interval_hours) VALUES('edge',2,true,6)",
		"INSERT INTO image_update_registry_credentials(node_id,registry,credential_id) VALUES('edge','registry.example.test',51)",
		"INSERT INTO tasks(id,type,name,status) VALUES('retained-task','compose.update','Retained','pending')",
		"INSERT INTO audit_logs(action,result) VALUES('compose.update','success')",
		"INSERT INTO notification_channels(id,config_json,secret_ciphertext) VALUES('channel','{\"language\":\"zh-CN\"}',decode('ccdd','hex'))",
		"INSERT INTO notification_deliveries(id,channel_id,status,snapshot_json) VALUES('queued','channel','pending','snapshot'),('sent','channel','sent','historical')",
	} {
		if _, err := connection.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	tables := []string{"users", "nodes", "settings", "registry_credentials", "image_update_policies", "image_update_registry_credentials", "tasks", "audit_logs", "notification_channels", "notification_deliveries"}
	snapshot := func(table string) string {
		t.Helper()
		var value string
		if err := connection.QueryRow("SELECT COALESCE(jsonb_agg(value ORDER BY value::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS value FROM " + table + " t) rows").Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := map[string]string{}
	for _, name := range tables {
		before[name] = snapshot(name)
	}
	raw, err := os.ReadFile("migrations/0006_project_image_updates.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := connection.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, name := range tables {
		if snapshot(name) != before[name] {
			t.Errorf("migration changed retained %s", name)
		}
	}
	for _, name := range []string{"project_image_updates", "project_image_candidates", "project_image_actions"} {
		var count int
		if err := connection.QueryRow("SELECT count(*) FROM " + name).Scan(&count); err != nil || count != 0 {
			t.Fatal("upgrade fabricated policy or history", name, count, err)
		}
	}
}
func TestProjectImageUpdateFreshInitialization(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.ProjectImageUpdate{}, &database.ProjectImageCandidate{}, &database.ProjectImageAction{}} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing table %T", model)
		}
	}
}
