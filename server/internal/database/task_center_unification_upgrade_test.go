package database_test

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
)

func TestTaskCenterUnificationUpgradePreservesEvidence(t *testing.T) {
	for _, baseline := range []string{"0010_remove_image_updates.sql", "0011_task_center.sql"} {
		t.Run(baseline, func(t *testing.T) {
			dsn := testutil.DSN(t)
			connection, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			exec := func(statement string, args ...any) {
				t.Helper()
				if _, err := connection.Exec(statement, args...); err != nil {
					t.Fatal("fixture or migration failed", err)
				}
			}
			exec("CREATE TABLE schema_migrations (key varchar(128) PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())")
			entries, err := os.ReadDir("migrations")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() > baseline || !strings.HasSuffix(entry.Name(), ".sql") {
					continue
				}
				raw, err := os.ReadFile("migrations/" + entry.Name())
				if err != nil {
					t.Fatal(err)
				}
				exec(string(raw))
				exec("INSERT INTO schema_migrations(key) VALUES($1)", entry.Name())
			}
			for _, statement := range []string{
				"INSERT INTO users(id,username,password_hash) VALUES(41,'retained','retained-hash')",
				"INSERT INTO nodes(id,name,connection_type,endpoint) VALUES('edge','Retained','unix','unix:///fixture.sock')",
				"INSERT INTO git_credentials(id,name,auth_type,secret_ciphertext) VALUES(51,'Retained','http_token',decode('deadbeef','hex'))",
				"INSERT INTO settings(key,value) VALUES('retained.setting','keep')",
				"INSERT INTO tasks(id,type,name,status) VALUES('queued','cd.sync','Retained','pending'),('finished','image.update','Retained','success')",
				"INSERT INTO audit_logs(action,result,details) VALUES('retained','success','keep')",
				"INSERT INTO cleanup_policies(node_id,version,enabled,config_json,authorized_by,authorized_at,next_run_at) VALUES('edge',7,false,'{\"enabled\":false}',41,now(),NULL)",
				"INSERT INTO delivery_projects(id,name,deployment_name,git_clone_url,git_credential_id,automation_enabled,sync_interval_seconds,webhook_enabled,webhook_secret) VALUES(61,'retained','retained-runtime','https://git.example.test/app.git',51,false,35,true,decode('aabb','hex'))",
				"INSERT INTO project_cds(id,node_id,project_name,enabled,automation_enabled,sync_interval_seconds,webhook_id,credential_ciphertext,environment_ciphertext,failed_fingerprint) VALUES(71,'edge','retained',true,false,37,'retained-hook',decode('ccdd','hex'),decode('eeff','hex'),'retained-failure')",
				"INSERT INTO project_cd_records(project_cd_id,operation,trigger,status,revision,snapshot_ciphertext) VALUES(71,'check','manual','succeeded',1,decode('abcd','hex'))",
				"INSERT INTO notification_channels(id,config_json,secret_ciphertext) VALUES('channel','retained-config',decode('eeff','hex'))",
				"INSERT INTO notification_deliveries(id,channel_id,status,snapshot_json) VALUES('queued-delivery','channel','pending','queued-snapshot'),('sent-delivery','channel','sent','historical-snapshot')",
			} {
				exec(statement)
			}
			if baseline == "0011_task_center.sql" {
				exec("UPDATE tasks SET params = '{\"project\":\"retained\"}', result = '{\"updated\":[\"web\"]}' WHERE id = 'finished'")
				exec("INSERT INTO task_center_schedules(id,name,kind,node_id,node_name,subject,params,cron,timezone,enabled,version) VALUES(7,'Retained','image.update','edge','Retained','retained','{\"project\":\"retained\",\"auto_apply\":false}','0 4 * * *','UTC',false,9)")
				exec("INSERT INTO task_center_schedule_runs(id,schedule_id,task_id,error) VALUES(11,7,'finished',''),(12,7,'','original failure'),(13,7,'','')")
				exec("INSERT INTO task_center_artifacts(task_id,kind,node_id,subject,data) VALUES('finished','image.update','edge','retained','{\"updated\":[\"web\"]}')")
			}
			tables := []string{"users", "nodes", "git_credentials", "settings", "tasks", "audit_logs", "cleanup_policies", "delivery_projects", "project_cds", "project_cd_records", "notification_channels", "notification_deliveries"}
			if baseline == "0011_task_center.sql" {
				tables = append(tables, "task_center_schedules", "task_center_schedule_runs", "task_center_artifacts")
			}
			snapshot := func(table string) string {
				t.Helper()
				projection := "to_jsonb(t)"
				switch table {
				case "delivery_projects":
					projection += " - 'initial_application_required'"
				case "project_cd_records":
					projection += " - 'images_pinned'"
				case "tasks":
					projection += " - 'parent_task_id' - 'result_error'"
					if baseline != "0011_task_center.sql" {
						projection += " - 'params' - 'result'"
					}
				case "task_center_schedules":
					projection += " - 'schedule_type' - 'interval_seconds' - 'timer_enabled' - 'webhook_enabled' - 'archived_at'"
				case "task_center_schedule_runs":
					projection += " - 'status' - 'reason_code' - 'source_id' - 'scheduled_for' - 'actor_id' - 'rule_snapshot' - 'config_version'"
				}
				var result string
				if err := connection.QueryRow("SELECT COALESCE(jsonb_agg(value ORDER BY value::text),'[]'::jsonb)::text FROM (SELECT " + projection + " AS value FROM " + table + " t) rows").Scan(&result); err != nil {
					t.Fatal(err)
				}
				return result
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
			if baseline == "0011_task_center.sql" {
				for id, status := range map[uint]string{11: "started", 12: "start_failed", 13: "interrupted"} {
					var run database.TaskCenterScheduleRun
					if err := db.First(&run, id).Error; err != nil || run.Status != status || len(run.RuleSnapshot) != 0 || run.ActorID != nil || run.ScheduledFor != nil {
						t.Fatalf("fabricated or missing legacy evidence: %+v, %v", run, err)
					}
				}
				if _, err := connection.Exec("DELETE FROM task_center_schedules WHERE id = 7"); err == nil {
					t.Fatal("rule deletion erased trigger evidence")
				}
				exec("UPDATE task_center_schedules SET archived_at = now() WHERE id = 7")
				exec("INSERT INTO task_center_schedules(name,kind,node_id,subject,cron,timezone,enabled) VALUES('Replacement','image.update','edge','retained','0 4 * * *','UTC',false)")
			}
			row := database.TaskCenterSchedule{Name: "Disabled", Kind: "image.update", NodeID: "edge", Subject: "disabled", Params: database.JSONB(`{}`), Cron: "0 4 * * *", Timezone: "UTC", Enabled: false, TimerEnabled: false}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			var saved database.TaskCenterSchedule
			if err := db.First(&saved, row.ID).Error; err != nil || saved.Enabled || saved.TimerEnabled {
				t.Fatal("false was replaced by a model default", saved, err)
			}
			manual := database.TaskCenterScheduleRun{TriggeredBy: "manual", Status: "skipped", ReasonCode: "busy"}
			if err := db.Create(&manual).Error; err != nil {
				t.Fatal("ad hoc trigger requires a rule", err)
			}
		})
	}
}
