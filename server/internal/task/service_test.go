package task

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
)

func TestTaskLifecycle(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	row, err := service.Start("test", "Test task", func(_ context.Context, report Reporter) error { report(50, "halfway"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var current database.Task
		db.First(&current, "id = ?", row.ID)
		if current.Status == StatusSuccess {
			logs, _ := service.Logs(context.Background(), row.ID)
			if len(logs) < 2 {
				t.Fatal("expected task logs")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("task did not complete")
}

func TestTaskStepsAreUpserted(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	service.ReportStep(context.Background(), "task-1", "layer-a", "Downloading", 5, 10, 40)
	service.ReportStep(context.Background(), "task-1", "layer-a", "Pull complete", 10, 10, 100)
	steps, err := service.Steps(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Status != "Pull complete" || steps[0].Progress != 100 || steps[0].Current != 10 {
		t.Fatalf("unexpected task steps: %+v", steps)
	}
}

func TestUpdateProgressDoesNotAppendTaskLog(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	row := database.Task{ID: "task-1", Scope: ScopeNode, NodeID: "local", Type: "compose.pull", Name: "Pull", Status: StatusRunning}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateProgress(context.Background(), row.ID, 42, "layer Downloading 10MB"); err != nil {
		t.Fatal(err)
	}
	var current database.Task
	if err := db.First(&current, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	logs, err := service.Logs(context.Background(), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Progress != 42 || current.Message != "layer Downloading 10MB" || len(logs) != 0 {
		t.Fatalf("task = %#v, logs = %#v", current, logs)
	}
}

func TestStartDefinitionRecordsParamsAndResult(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	row, err := service.StartDefinition(context.Background(), Definition{
		Type: "image.test", Name: "Recorded", NodeID: "local", NodeName: "Local",
		Params: map[string]any{"project": "blog", "password": "private-value"},
	}, func(ctx context.Context, id string, report Reporter) (any, error) {
		report(50, "halfway")
		return map[string]any{"digest": "sha256:ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Wait(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := service.Get(context.Background(), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusSuccess {
		t.Fatalf("unexpected status: %s", stored.Status)
	}
	var params map[string]any
	if json.Unmarshal(stored.Params, &params) != nil || params["project"] != "blog" {
		t.Fatalf("input params must be recorded: %s", string(stored.Params))
	}
	if redacted, _ := params["password"].(string); redacted != "[REDACTED]" {
		t.Fatalf("sensitive params must be redacted: %s", string(stored.Params))
	}
	var result map[string]any
	if json.Unmarshal(stored.Result, &result) != nil || result["digest"] != "sha256:ok" {
		t.Fatalf("structured result must be recorded: %s", string(stored.Result))
	}
	failed, err := service.StartDefinition(context.Background(), Definition{Type: "image.test", Name: "Failing", Params: map[string]any{"project": "blog"}}, func(ctx context.Context, id string, report Reporter) (any, error) {
		return nil, errors.New("registry unreachable")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Wait(context.Background(), failed.ID); err != nil {
		t.Fatal(err)
	}
	stored, err = service.Get(context.Background(), failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusFailed || len(stored.Result) != 0 {
		t.Fatalf("failed task must not carry a result: %+v", stored)
	}
}
