package database_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
)

func TestDeliveryEnvironmentUpgradeRetainsProductionEvidence(t *testing.T) {
	dsn := testutil.DSN(t)
	connection, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, name := range []string{"0001_initial.sql", "0002_container_monitoring.sql", "0003_notification_incidents.sql", "0004_delivery_automation.sql"} {
		raw, err := os.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE schema_migrations (key varchar(128) PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())",
		"INSERT INTO schema_migrations(key) VALUES ('0001_initial.sql'),('0002_container_monitoring.sql'),('0003_notification_incidents.sql'),('0004_delivery_automation.sql')",
		"INSERT INTO users(id,username,password_hash) VALUES (41,'retained','retained-hash')",
		"INSERT INTO nodes(id,name,connection_type,endpoint) VALUES ('target','Retained','unix','unix:///fixture.sock')",
		"INSERT INTO git_credentials(id,name,auth_type,secret_ciphertext) VALUES (51,'Retained','http_token',decode('deadbeef','hex'))",
		"INSERT INTO settings(key,value) VALUES ('general.server_name','Retained server')",
		"INSERT INTO delivery_projects(id,name,deployment_name,git_clone_url,git_credential_id,reconcile_mode,webhook_id,webhook_enabled,webhook_secret,active_release_id,desired_commit) VALUES (61,'retained','retained-runtime','https://git.example.test/app.git',51,'auto','retained-hook',true,decode('aabb','hex'),71,'retained-commit')",
		"INSERT INTO delivery_project_nodes(project_id,node_id) VALUES (61,'target')",
		"INSERT INTO delivery_project_git_credentials(id,project_id,name,auth_type,secret_ciphertext) VALUES (52,61,'Project credential','http_token',decode('ccdd','hex'))",
		"INSERT INTO tasks(id,type,name,status) VALUES ('queued','cd.sync','Retained task','pending')",
		"INSERT INTO delivery_releases(id,project_id,commit_sha,status,trigger_type,task_id,worktree_path) VALUES (71,61,'active-commit','succeeded','manual','old-task','/retained/tree'),(72,61,'pending-commit','awaiting_approval','webhook','queued','/retained/pending')",
		"INSERT INTO audit_logs(id,action,result,details) VALUES (81,'cd.deploy','success','Retained audit')",
		"INSERT INTO notification_channels(id,config_json,secret_ciphertext) VALUES ('channel','retained-subscription',decode('eeff','hex'))",
		"INSERT INTO notification_deliveries(id,channel_id,status,snapshot_json) VALUES ('pending','channel','pending','queued-snapshot'),('sent','channel','sent','historical-snapshot')",
		"INSERT INTO git_webhook_deliveries(id,format,hook_id,delivery_id,status) VALUES (91,'generic','retained-hook','retained-delivery','queued')",
	} {
		if _, err := connection.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	tables := []string{"users", "nodes", "git_credentials", "settings", "delivery_projects", "delivery_project_nodes", "delivery_project_git_credentials", "tasks", "delivery_releases", "audit_logs", "notification_channels", "notification_deliveries", "git_webhook_deliveries", "schema_migrations"}
	snapshot := func(table string) string {
		t.Helper()
		var value string
		// Exclude only the new field and migration ledger entry being introduced.
		query := "SELECT COALESCE(jsonb_agg(value ORDER BY value::text), '[]'::jsonb)::text FROM (SELECT to_jsonb(t) - 'environment_ciphertext' AS value FROM " + table + " t"
		if table == "schema_migrations" {
			query += " WHERE key <> '0005_delivery_environment.sql'"
		}
		query += ") rows"
		if err := connection.QueryRow(query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := map[string]string{}
	for _, table := range tables {
		before[table] = snapshot(table)
	}
	db, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	for _, table := range tables {
		if snapshot(table) != before[table] {
			t.Errorf("upgrade changed retained %s evidence", table)
		}
	}
	var project database.DeliveryProject
	if err := db.First(&project, 61).Error; err != nil || len(project.EnvironmentCiphertext) != 0 {
		t.Fatal("upgrade fabricated manual environment", err)
	}
	var release database.DeliveryRelease
	if err := db.First(&release, 71).Error; err != nil || len(release.EnvironmentCiphertext) != 0 {
		t.Fatal("upgrade fabricated historical environment", err)
	}
	again, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	againPool, _ := again.DB()
	defer againPool.Close()
	if err := again.First(&project, 61).Error; err != nil || len(project.EnvironmentCiphertext) != 0 {
		t.Fatal("reopen changed manual environment", err)
	}
	var count int64
	again.Model(&database.SchemaMigration{}).Count(&count)
	if count != 5 {
		t.Fatal("migration was not recorded exactly once", count)
	}
}
