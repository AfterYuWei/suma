package task

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
	"gorm.io/gorm"
)

func TestResultEvidenceFailureDoesNotRewriteExecutionOutcome(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		result any
		code   string
	}{
		{"complete_large_result", map[string]string{"output": strings.Repeat("a", 90_000)}, ""},
		{"too_large", map[string]string{"output": strings.Repeat("a", ResultLimit)}, "result_too_large"},
		{"encoding_failure", make(chan int), "result_encoding_failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, err := testutil.Open(t)
			if err != nil {
				t.Fatal(err)
			}
			service := NewService(db)
			row, err := service.StartDefinition(context.Background(), Definition{Type: "controlled", Name: "Evidence"}, func(context.Context, string, Reporter) (any, error) { return scenario.result, nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Wait(context.Background(), row.ID); err != nil {
				t.Fatal(err)
			}
			stored, err := service.Get(context.Background(), row.ID)
			if err != nil || stored.Status != StatusSuccess || stored.FinishedAt == nil {
				t.Fatalf("execution outcome lost: %+v %v", stored, err)
			}
			if scenario.code == "" {
				if !json.Valid(stored.Result) || len(stored.Result) < 90_000 {
					t.Fatal("large evidence was lost")
				}
			} else if !strings.HasPrefix(stored.ResultError, scenario.code) || len(stored.Result) != 0 {
				t.Fatalf("evidence error missing: %+v", stored)
			}
		})
	}
}

func TestTaskAndTriggerAreCreatedAtomically(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	var executions int
	work := func(context.Context, string, Reporter) (any, error) { executions++; return nil, nil }
	if _, err := service.StartDefinition(context.Background(), Definition{Type: "controlled", Name: "Invalid trigger", TriggerRunID: 999}, work); err == nil {
		t.Fatal("missing trigger was accepted")
	}
	var count int64
	if err := db.Model(&database.Task{}).Count(&count).Error; err != nil || count != 0 || executions != 0 {
		t.Fatal("failed admission left a task or executed work")
	}
	run := database.TaskCenterScheduleRun{Status: "dispatching", TriggeredBy: "manual"}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	row, err := service.StartDefinition(context.Background(), Definition{Type: "controlled", Name: "Linked", TriggerRunID: run.ID, ParentTaskID: "parent"}, work)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Wait(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	var bound database.TaskCenterScheduleRun
	if err := db.First(&bound, run.ID).Error; err != nil || bound.Status != "started" || bound.TaskID != row.ID {
		t.Fatalf("trigger was not linked: %+v %v", bound, err)
	}
	stored, err := service.Get(context.Background(), row.ID)
	if err != nil || stored.ParentTaskID != "parent" || executions != 1 {
		t.Fatal("parent linkage or work was lost")
	}
}

func TestArtifactFailureRetainsTaskResultAndTerminalState(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Create().Before("gorm:create").Register("fail_artifact", func(tx *gorm.DB) {
		if tx.Statement.Table == "task_center_artifacts" {
			tx.AddError(errors.New("controlled storage failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	row, err := service.StartDefinition(context.Background(), Definition{Type: "image.update", Name: "Projected", ArtifactSubject: "blog"}, func(context.Context, string, Reporter) (any, error) { return map[string]string{"project": "blog"}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Wait(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := service.Get(context.Background(), row.ID)
	if err != nil || stored.Status != StatusSuccess || !json.Valid(stored.Result) || stored.ResultError == "" {
		t.Fatalf("task evidence lost with projection: %+v %v", stored, err)
	}
}

func TestOversizedParametersAreRejectedBeforeTaskCreation(t *testing.T) {
	service := NewService(nil)
	_, err := service.StartDefinition(context.Background(), Definition{Type: "controlled", Params: map[string]string{"value": strings.Repeat("x", paramsLimit)}}, func(context.Context, string, Reporter) (any, error) {
		t.Fatal("invalid parameters executed work")
		return nil, nil
	})
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("expected a boundary error, got %v", err)
	}
}
