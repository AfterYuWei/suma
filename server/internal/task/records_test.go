package task

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRecordSummarySQLDoesNotSelectPayloads(t *testing.T) {
	// This only validates SQL generation. PostgreSQL integration below remains
	// required for actual filtering, pagination and retained evidence behavior.
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=unused dbname=unused"}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	var rows []RecordView
	query := service.recordQuery(context.Background(), true).Find(&rows)
	sql := query.Statement.SQL.String()
	if query.Error != nil || strings.Contains(sql, "tasks.*") || strings.Contains(sql, "tasks.params") || strings.Contains(sql, "tasks.result,") || !strings.Contains(sql, "trigger_record.triggered_by") {
		t.Fatalf("summary unexpectedly selects payloads or loses trigger fields: %s / %v", sql, query.Error)
	}
	var total int64
	count := service.recordQuery(context.Background(), true).Count(&total)
	if count.Error != nil || !strings.HasPrefix(count.Statement.SQL.String(), `SELECT count(*) FROM "tasks"`) {
		t.Fatalf("joined record count is invalid: %s / %v", count.Statement.SQL.String(), count.Error)
	}
}

func TestRecordFiltersUseRecordedSourceAndPreserveArchivedEvidence(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	service := NewService(db)
	now := time.Now()
	rule := database.TaskCenterSchedule{Name: "Global delivery", Kind: "cd.sync", Subject: "blog", Params: database.JSONB(`{"project":"blog"}`), ScheduleType: "interval", IntervalSeconds: 30, Timezone: "UTC", Version: 3, ArchivedAt: &now}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	rows := []database.Task{
		{ID: "parent", Scope: ScopeControlPlane, Name: "Release 100%_done", Type: "cd.sync", Status: StatusSuccess, Result: database.JSONB(`{"large":"evidence"}`)},
		{ID: "child", Scope: ScopeNode, NodeID: "removed-node", NodeName: "Old Engine", ParentTaskID: "parent", Name: "Deploy blog", Type: "cd.deploy", Status: StatusFailed},
		{ID: "unknown", Scope: ScopeNode, NodeID: "removed-node", Name: "Release 100X_done", Type: "compose.update", Status: StatusSuccess},
		{ID: "direct", Scope: ScopeNode, NodeID: "removed-node", ParentTaskID: "parent", Name: "Direct operation", Type: "cd.deploy", Status: StatusSuccess},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	runs := []database.TaskCenterScheduleRun{
		{ScheduleID: &rule.ID, TaskID: "parent", TriggeredBy: "scheduled", Status: "started"},
		{TaskID: "direct", TriggeredBy: "manual", Status: "started"},
	}
	if err := db.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name  string
		query RecordQuery
		ids   []string
	}{
		{"inherited source and archived rule", RecordQuery{Scope: ScopeNode, NodeID: "removed-node", Source: "scheduled", ScheduleID: rule.ID}, []string{"child"}},
		{"own trigger takes precedence", RecordQuery{Source: "manual"}, []string{"direct"}},
		{"missing source stays unknown", RecordQuery{Source: "unrecorded"}, []string{"unknown"}},
		{"literal wildcard search", RecordQuery{Search: "100%_done"}, []string{"parent"}},
		{"case insensitive node search", RecordQuery{Search: "old engine", Status: "failed"}, []string{"child"}},
		{"literal type prefix", RecordQuery{TypePrefix: "cd.%"}, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			found, total, err := service.Records(ctx, scenario.query)
			if err != nil || total != int64(len(scenario.ids)) || len(found) != len(scenario.ids) {
				t.Fatalf("unexpected records: %+v / %d / %v", found, total, err)
			}
			for i, id := range scenario.ids {
				if found[i].ID != id {
					t.Fatalf("got %s, expected %s", found[i].ID, id)
				}
			}
		})
	}
	page, total, err := service.Records(ctx, RecordQuery{Source: "scheduled", Summary: true, Limit: 1, Offset: 1})
	if err != nil || total != 2 || len(page) != 1 || len(page[0].Result) != 0 || len(page[0].Params) != 0 {
		t.Fatalf("summary pagination lost: %+v / %d / %v", page, total, err)
	}
	summary, _, err := service.Records(ctx, RecordQuery{Scope: ScopeControlPlane, Summary: true})
	if err != nil || len(summary) != 1 || len(summary[0].Result) != 0 {
		t.Fatalf("large result entered the summary query: %+v / %v", summary, err)
	}
	child, err := service.Record(ctx, "child")
	if err != nil || child.RunID != runs[0].ID || child.ScheduleID == nil || *child.ScheduleID != rule.ID || child.ScheduleName != rule.Name || child.TriggeredBy != "scheduled" {
		t.Fatalf("parent source evidence lost: %+v / %v", child, err)
	}
}
