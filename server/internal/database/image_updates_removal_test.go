package database_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/suma/suma/server/internal/testutil"
)

// Upgrading from the previous production schema must drop only the image
// update feature tables and retain every other row, including the migration
// ledger, users, credentials, tasks, audit and notification evidence.
func TestImageUpdateRemovalUpgradeRetainsExistingData(t *testing.T) {
	dsn := testutil.DSN(t)
	connection, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, name := range []string{"0001_initial.sql", "0002_container_monitoring.sql", "0003_notification_incidents.sql", "0004_delivery_automation.sql", "0005_delivery_environment.sql", "0006_project_image_updates.sql", "0007_delivery_release_records.sql", "0008_project_cd.sql", "0009_project_cd_policy.sql"} {
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
		"INSERT INTO project_image_updates(node_id,project) VALUES('edge','shop')",
		"INSERT INTO project_image_candidates(id,node_id,project,services_json,status,created_at,updated_at) VALUES('candidate','edge','shop','[]','pending',now(),now())",
		"INSERT INTO project_image_actions(token_hash,candidate_id,delivery_id,channel_id,chat_id,created_at) VALUES(decode('aabb','hex'),'candidate','delivery','channel','chat',now())",
		"INSERT INTO tasks(id,type,name,status) VALUES('retained-task','image.updates.check','Retained','pending')",
		"INSERT INTO audit_logs(action,result) VALUES('image.updates.check','success')",
		"INSERT INTO notification_channels(id,config_json,secret_ciphertext) VALUES('channel','{\"language\":\"zh-CN\"}',decode('ccdd','hex'))",
		"INSERT INTO notification_deliveries(id,channel_id,status,snapshot_json) VALUES('queued','channel','pending','snapshot'),('sent','channel','sent','historical')",
	} {
		if _, err := connection.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	tables := []string{"users", "nodes", "settings", "registry_credentials", "tasks", "audit_logs", "notification_channels", "notification_deliveries", "schema_migrations"}
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
	raw, err := os.ReadFile("migrations/0010_remove_image_updates.sql")
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
	for _, name := range []string{"image_update_policies", "image_update_registry_credentials", "project_image_updates", "project_image_candidates", "project_image_actions"} {
		var exists bool
		if err := connection.QueryRow("SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)", name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("removed image update table still exists: %s", name)
		}
	}
}
