package database_test

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
)

func TestReleasePlansInitializeAndUpgradeWithoutInventingImageEvidence(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		name := "empty"
		if upgrade {
			name = "from-0012"
		}
		t.Run(name, func(t *testing.T) {
			dsn := testutil.DSN(t)
			conn, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			exec := func(query string) {
				t.Helper()
				if _, err := conn.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			var before string
			snapshot := func() string {
				t.Helper()
				var value string
				if err := conn.QueryRow(`SELECT jsonb_build_object('users',(SELECT jsonb_agg(t ORDER BY id) FROM users t),'nodes',(SELECT jsonb_agg(t ORDER BY id) FROM nodes t),'credentials',(SELECT jsonb_agg(t ORDER BY id) FROM git_credentials t),'settings',(SELECT jsonb_agg(t ORDER BY key) FROM settings t),'projects',(SELECT jsonb_agg(to_jsonb(t)-'initial_application_required' ORDER BY id) FROM delivery_projects t),'records',(SELECT jsonb_agg(to_jsonb(t)-'images_pinned'-'frozen_input_ciphertext' ORDER BY id) FROM delivery_records t),'single_records',(SELECT jsonb_agg(to_jsonb(t)-'images_pinned' ORDER BY id) FROM project_cd_records t),'tasks',(SELECT jsonb_agg(t ORDER BY id) FROM tasks t),'notifications',(SELECT jsonb_agg(t ORDER BY id) FROM notification_deliveries t))::text`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			if upgrade {
				exec("CREATE TABLE schema_migrations (key varchar(128) PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())")
				entries, err := os.ReadDir("migrations")
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() >= "0013" {
						continue
					}
					raw, err := os.ReadFile("migrations/" + entry.Name())
					if err != nil {
						t.Fatal(err)
					}
					exec(string(raw))
					if _, err := conn.Exec("INSERT INTO schema_migrations(key) VALUES($1)", entry.Name()); err != nil {
						t.Fatal(err)
					}
				}
				exec("INSERT INTO users(id,username,password_hash) VALUES(41,'retained','retained-hash')")
				exec("INSERT INTO nodes(id,name,connection_type,endpoint) VALUES('retained','Retained','unix','unix:///fixture.sock')")
				exec("INSERT INTO git_credentials(id,name,auth_type,secret_ciphertext) VALUES(51,'Retained','http_token',decode('aabb','hex'))")
				exec("INSERT INTO settings(key,value) VALUES('general.server_name','Retained')")
				exec("INSERT INTO delivery_projects(id,name,deployment_name,git_clone_url,reconcile_mode,environment_ciphertext) VALUES(61,'retained','retained','https://example.test/app.git','auto',decode('ccdd','hex'))")
				exec("INSERT INTO delivery_records(id,project_id,release_id,operation,status,snapshot_complete,environment_ciphertext,image_references) VALUES(71,61,81,'deploy','succeeded',true,decode('eeff','hex'),'[\"example/app:latest\"]')")
				exec("INSERT INTO project_cds(id,node_id,project_name,revision,webhook_id) VALUES(91,'retained','app',7,'retained-hook')")
				exec("INSERT INTO project_cd_records(id,project_cd_id,operation,trigger,status,revision,snapshot_ciphertext) VALUES(101,91,'apply','manual','succeeded',7,decode('abcd','hex'))")
				exec("INSERT INTO tasks(id,type,name,status) VALUES('retained-task','cd.deploy','Retained','pending')")
				exec("INSERT INTO notification_channels(id,config_json,secret_ciphertext) VALUES('retained-channel','retained-subscription',decode('ab12','hex'))")
				exec("INSERT INTO notification_deliveries(id,channel_id,status,snapshot_json) VALUES('retained-pending','retained-channel','pending','queued-snapshot'),('retained-sent','retained-channel','sent','historical-snapshot')")
				before = snapshot()
			}
			for range 2 {
				db, err := database.Open(dsn)
				if err != nil {
					t.Fatal(err)
				}
				pool, err := db.DB()
				if err != nil {
					t.Fatal(err)
				}
				defer pool.Close()
				if upgrade && snapshot() != before {
					t.Fatal("upgrade changed existing credentials, settings, configuration or evidence")
				}
				if !db.Migrator().HasTable(&database.ReleasePlan{}) {
					t.Fatal("release plans table missing")
				}
				if upgrade {
					var record database.DeliveryRecord
					var single database.ProjectCDRecord
					var project database.DeliveryProject
					if err := db.First(&record, 71).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.First(&single, 101).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.First(&project, 61).Error; err != nil {
						t.Fatal(err)
					}
					if record.ImagesPinned || single.ImagesPinned || len(record.FrozenInputCiphertext) > 0 || project.InitialApplicationRequired {
						t.Fatal("migration invented historical image or approval evidence")
					}
				}
			}
		})
	}
}
