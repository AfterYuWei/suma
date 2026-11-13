package database_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/suma/suma/server/internal/testutil"
)

// Upgrading from the previous production schema must add the Task Center
// tables and task parameter/result columns without touching any retained
// row: accounts, nodes, tasks, audit and notification evidence survive.
func TestTaskCenterUpgradeRetainsExistingData(t *testing.T) {
	dsn := testutil.DSN(t)
	connection, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, name := range []string{"0001_initial.sql", "0002_container_monitoring.sql", "0003_notification_incidents.sql", "0004_delivery_automation.sql", "0005_delivery_environment.sql", "0006_project_image_updates.sql", "0007_delivery_release_records.sql", "0008_project_cd.sql", "0009_project_cd_policy.sql", "0010_remove_image_updates.sql"} {
		raw, err := os.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"INSERT INTO users(id,username,password_hash) VALUES(43,'retained','password-hash')",
		"INSERT INTO nodes(id,name,connection_type,endpoint) VALUES('edge','Retained','unix','unix:///fixture.sock')",
		"INSERT INTO tasks(id,type,name,status) VALUES('retained-task','image.updates.check','Retained','pending')",
		"INSERT INTO audit_logs(action,result) VALUES('image.updates.check','success')",
	} {
		if _, err := connection.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func(query string) string {
		t.Helper()
		var value string
		if err := connection.QueryRow(query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot("SELECT COALESCE(jsonb_agg(value ORDER BY value::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) - 'params' - 'result' AS value FROM tasks t) rows")
	beforeAudit := snapshot("SELECT COALESCE(jsonb_agg(value ORDER BY value::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS value FROM audit_logs t) rows")
	raw, err := os.ReadFile("migrations/0011_task_center.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	if snapshot("SELECT COALESCE(jsonb_agg(value ORDER BY value::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) - 'params' - 'result' AS value FROM tasks t) rows") != before {
		t.Error("0011 changed retained tasks rows")
	}
	if snapshot("SELECT COALESCE(jsonb_agg(value ORDER BY value::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS value FROM audit_logs t) rows") != beforeAudit {
		t.Error("0011 changed retained audit rows")
	}
	for _, table := range []string{"task_center_schedules", "task_center_schedule_runs", "task_center_artifacts"} {
		var exists bool
		if err := connection.QueryRow("SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)", table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("task center table missing after upgrade: %s", table)
		}
	}
	for _, column := range []string{"params", "result"} {
		var exists bool
		if err := connection.QueryRow("SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'tasks' AND column_name = $1)", column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("tasks.%s column missing after upgrade", column)
		}
	}
	for _, statement := range []string{
		"INSERT INTO task_center_schedules(id,name,kind,node_id,node_name,subject,params,cron,timezone,enabled) VALUES(7,'blog updates','image.update','edge','Retained','blog','{\"project\":\"blog\",\"auto_apply\":false}','0 4 * * *','UTC',true)",
		"INSERT INTO task_center_schedule_runs(schedule_id,task_id,triggered_by) VALUES(7,'retained-task','scheduled')",
		"UPDATE tasks SET params = '{\"project\":\"blog\"}', result = '{\"updated\":[\"web\"]}' WHERE id = 'retained-task'",
		"INSERT INTO task_center_artifacts(task_id,kind,node_id,subject,data) VALUES('retained-task','image.update','edge','blog','{\"project\":\"blog\"}')",
	} {
		if _, err := connection.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var name, project string
	if err := connection.QueryRow("SELECT s.name, r.task_id FROM task_center_schedules s JOIN task_center_schedule_runs r ON r.schedule_id = s.id WHERE s.id = 7").Scan(&name, &project); err != nil {
		t.Fatal(err)
	}
	if name != "blog updates" || project != "retained-task" {
		t.Fatal("task center upgrade round trip failed")
	}
	// The unique plan identity must reject a second plan for the same target.
	if _, err := connection.Exec("INSERT INTO task_center_schedules(id,name,kind,node_id,node_name,subject,params,cron,timezone) VALUES(8,'dup','image.update','edge','Retained','blog','{}','0 4 * * *','UTC')"); err == nil {
		t.Fatal("duplicate plan identity was accepted")
	}
}
