package cd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
)

func TestDeliveryHistoryCombinesExistingSourcesWithoutChangingOwnership(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	batch := database.DeliveryProject{Name: "shared"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	release := database.DeliveryRelease{ProjectID: batch.ID, CommitSHA: strings.Repeat("a", 40)}
	if err := db.Create(&release).Error; err != nil {
		t.Fatal(err)
	}
	record := database.DeliveryRecord{ID: 1, ProjectID: batch.ID, ReleaseID: release.ID, Operation: "deploy", Status: "succeeded", NodeIDsJSON: `["one","two"]`, EnvironmentCiphertext: []byte("private-batch-environment"), CreatedAt: created}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Node{ID: "one", Name: "Renamed node", ConnectionType: "unix", Endpoint: "unix:///fixture.sock"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Task{ID: "node-task", NodeID: "one", NodeName: "Original node", Type: "project.cd.apply", Status: "success"}).Error; err != nil {
		t.Fatal(err)
	}
	one := database.ProjectCD{NodeID: "one", ProjectName: "shared", WebhookID: "one", EnvironmentCiphertext: []byte("private-local-environment")}
	two := database.ProjectCD{NodeID: "deleted-node", ProjectName: "shared", WebhookID: "two"}
	for _, row := range []*database.ProjectCD{&one, &two} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, operation := range []string{"apply", "check", "rollback", "auto_rollback", "detach"} {
		row := database.ProjectCDRecord{ID: uint(i + 1), ProjectCDID: one.ID, Operation: operation, Status: "succeeded", CreatedAt: created, CommitSHA: strings.Repeat("b", 40), SnapshotCiphertext: []byte("private-snapshot"), Revision: 1}
		if i == 0 {
			row.TaskID = "node-task"
		}
		if operation == "check" || operation == "auto_rollback" {
			row.Status, row.ProjectCDID = "failed", two.ID
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(db, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()
	if err := db.Model(&release).Updates(map[string]any{"task_id": "candidate-check", "status": "awaiting_approval"}).Error; err != nil {
		t.Fatal(err)
	}
	candidates, err := s.ReleasesForTask(ctx, "candidate-check")
	if err != nil || len(candidates) != 1 || candidates[0].ID != release.ID || candidates[0].ProjectName != batch.Name || candidates[0].Status != "awaiting_approval" {
		t.Fatal("candidate check lost original release evidence", err)
	}
	payload, err := json.Marshal(map[string]any{"release_id": release.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Task{ID: "candidate-check", Type: "cd.sync", Name: "Original check", Status: "success", Result: database.JSONB(payload)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&release).Update("task_id", "later-deployment").Error; err != nil {
		t.Fatal(err)
	}
	candidates, err = s.ReleasesForTask(ctx, "candidate-check")
	if err != nil || len(candidates) != 1 || candidates[0].ID != release.ID {
		t.Fatal("later deployment erased original check association", err)
	}
	checks, err := s.ListHistory(ctx, HistoryQuery{TaskID: "candidate-check"})
	if err != nil || checks.Total != 0 {
		t.Fatal("candidate check fabricated an operation record", err)
	}
	// Independent record tables can both contain ID 1. A stable Task reference
	// must select only the original business namespace, including node work.
	linkedTasks := []database.Task{
		{ID: "cd-node-reference", Type: "cd.deploy.node", Status: "success", Params: database.JSONB(`{"record_id":1}`)},
		{ID: "project-reference", Type: "project.cd.check", Status: "success", Result: database.JSONB(`{"record_id":1}`)},
	}
	if err := db.Create(&linkedTasks).Error; err != nil {
		t.Fatal(err)
	}
	for taskID, source := range map[string]string{"cd-node-reference": "continuous_delivery", "project-reference": "project_cd"} {
		linked, err := s.ListHistory(ctx, HistoryQuery{TaskID: taskID})
		if err != nil || linked.Total != 1 || linked.Items[0].Source != source || linked.Items[0].RecordID != 1 {
			t.Fatalf("stable Task reference crossed business ownership: %+v %v", linked, err)
		}
	}
	first, err := s.ListHistory(ctx, HistoryQuery{Page: 1, PageSize: 2})
	if err != nil || first.Total != 6 || len(first.Items) != 2 || first.Items[0].ID != "continuous_delivery:1" || first.Items[1].ID != "project_cd:5" {
		t.Fatalf("first merged page: %+v, %v", first, err)
	}
	second, err := s.ListHistory(ctx, HistoryQuery{Page: 2, PageSize: 2})
	if err != nil || second.Total != 6 || second.Items[0].ID != "project_cd:4" || second.Items[1].ID != "project_cd:3" {
		t.Fatalf("unstable merged pagination: %+v, %v", second, err)
	}
	for _, test := range []struct {
		query HistoryQuery
		count int64
	}{
		{HistoryQuery{TaskID: "node-task"}, 1},
		{HistoryQuery{TaskID: "missing-task"}, 0},
		{HistoryQuery{TaskID: "cd-node-reference", Source: "project_cd"}, 0},
		{HistoryQuery{TaskID: "project-reference", Source: "continuous_delivery"}, 0},
		{HistoryQuery{Source: "project_cd"}, 5},
		{HistoryQuery{Operation: "deploy"}, 2},
		{HistoryQuery{Source: "project_cd", Operation: "deploy"}, 1},
		{HistoryQuery{Operation: "manual_rollback"}, 1},
		{HistoryQuery{Status: "failed"}, 2},
		{HistoryQuery{Source: "continuous_delivery", Status: "failed"}, 0},
		{HistoryQuery{Search: "Original node"}, 1},
		{HistoryQuery{Search: "Renamed node"}, 3},
		{HistoryQuery{Search: "two"}, 1},
		{HistoryQuery{Search: "deleted-node"}, 2},
		{HistoryQuery{Search: "%"}, 0},
		{HistoryQuery{Search: "' OR 1=1--"}, 0},
		{HistoryQuery{Page: 99}, 6},
	} {
		page, err := s.ListHistory(ctx, test.query)
		if err != nil || page.Total != test.count {
			t.Fatalf("query %+v: count=%d error=%v", test.query, page.Total, err)
		}
	}
	projects, err := s.ListHistory(ctx, HistoryQuery{Source: "project_cd", Operation: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if projects.Items[0].ReleaseID != nil || projects.Items[0].NodeID != "one" || projects.Items[0].NodeName != "Original node" || !projects.Items[0].NodeExists {
		t.Fatal("project ownership or historical node name was lost", projects.Items[0])
	}
	encoded, _ := json.Marshal(projects)
	if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "ciphertext") {
		t.Fatal("history exposed private snapshots or variables")
	}
	var count int64
	if err := db.Model(&database.DeliveryRelease{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("history created a batch release", err)
	}
	if err := db.Model(&database.DeliveryRecord{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("history copied a project record into batch ownership", err)
	}
}
