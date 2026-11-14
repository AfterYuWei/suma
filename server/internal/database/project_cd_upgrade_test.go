package database_test

import (
	"database/sql"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
	"os"
	"strings"
	"testing"
)

func TestProjectCDUpgradePreservesPreviousSchema(t *testing.T) {
	dsn := testutil.DSN(t)
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE TABLE schema_migrations (key varchar(128) PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "0008" || !strings.HasSuffix(entry.Name(), ".sql") {
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
	for _, statement := range []string{"INSERT INTO users(id,username,password_hash) VALUES(41,'retained','hash')", "INSERT INTO nodes(id,name,connection_type,endpoint) VALUES('retained','Retained','unix','unix:///retained.sock')", "INSERT INTO git_credentials(id,name,auth_type,secret_ciphertext) VALUES(51,'Retained','http_token',decode('deadbeef','hex'))", "INSERT INTO settings(key,value) VALUES('general.server_name','Retained')", "INSERT INTO tasks(id,type,name,status) VALUES('retained-task','cd.sync','Retained task','pending')", "INSERT INTO audit_logs(action,result,details) VALUES('cd.deploy','success','Retained evidence')",
		"INSERT INTO delivery_projects(id,name,deployment_name,git_credential_id) VALUES(61,'batch-retained','batch-retained',51)",
		"INSERT INTO delivery_project_nodes(project_id,node_id) VALUES(61,'retained')",
		"INSERT INTO delivery_releases(id,project_id,repository_url,commit_sha,status,trigger_type,task_id) VALUES(71,61,'https://git.example.test/retained.git','retained-commit','succeeded','manual','retained-task')",
		"INSERT INTO delivery_records(id,project_id,release_id,operation,status,git_credential_id,environment_ciphertext) VALUES(81,61,71,'deploy','succeeded',51,decode('aabb','hex'))",
		"INSERT INTO notification_channels(id,config_json,secret_ciphertext) VALUES('retained-channel','retained-subscription',decode('ccdd','hex'))",
		"INSERT INTO notification_deliveries(id,channel_id,status,snapshot_json) VALUES('retained-delivery','retained-channel','pending','retained-evidence')",
	} {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		var result string
		if err := conn.QueryRow("SELECT jsonb_build_object('users',(SELECT jsonb_agg(t) FROM users t),'nodes',(SELECT jsonb_agg(t) FROM nodes t),'credentials',(SELECT jsonb_agg(t) FROM git_credentials t),'settings',(SELECT jsonb_agg(t) FROM settings t),'tasks',(SELECT jsonb_agg(to_jsonb(t) - 'params' - 'result' - 'parent_task_id' - 'result_error') FROM tasks t),'audit',(SELECT jsonb_agg(t) FROM audit_logs t),'projects',(SELECT jsonb_agg(to_jsonb(t)-'initial_application_required') FROM delivery_projects t),'releases',(SELECT jsonb_agg(t) FROM delivery_releases t),'records',(SELECT jsonb_agg(to_jsonb(t)-'frozen_input_ciphertext'-'images_pinned') FROM delivery_records t),'notifications',(SELECT jsonb_agg(t) FROM notification_deliveries t),'channels',(SELECT jsonb_agg(t) FROM notification_channels t))::text").Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	db, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	if before != snapshot() {
		t.Fatal("upgrade changed retained state")
	}
	var count int64
	if err := db.Model(&database.ProjectCD{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("existing projects were converted", err)
	}
	again, err := database.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	againPool, _ := again.DB()
	defer againPool.Close()
	if before != snapshot() {
		t.Fatal("reopen changed existing data")
	}
}
func TestProjectCDFreshIdentityAndDefaults(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{"one", "two"} {
		row := database.ProjectCD{NodeID: node, ProjectName: "same", WebhookID: node}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.ProjectCD{NodeID: "one", ProjectName: "same", WebhookID: "another"}).Error; err == nil {
		t.Fatal("duplicate node project identity accepted")
	}
}
