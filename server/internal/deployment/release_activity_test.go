package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/testutil"
)

func TestReleaseActivityRestoresOnlyCurrentOwnerAndValidPlan(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for index, kind := range []string{"batch", "project"} {
		t.Run(kind, func(t *testing.T) {
			owner, node, prefix := uint(index+1), "", "cd."
			if kind == "project" {
				node, prefix = "edge", "project.cd."
			}
			name := "app-" + kind
			params := database.JSONB(fmt.Sprintf(`{"project":%q}`, name))
			prepareID := kind + "-prepare"
			for _, row := range []database.Task{
				{ID: prepareID, Type: prefix + "prepare", NodeID: node, Name: name, Status: "success", Params: params},
				{ID: kind + "-other-node", Type: prefix + "prepare", NodeID: "another-node", Name: name, Status: "running", Params: params},
				{ID: kind + "-other-project", Type: prefix + "prepare", NodeID: node, Name: name, Status: "running", Params: database.JSONB(`{"project":"another-project"}`)},
			} {
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			view := ReleasePlanView{Operation: "retry", SourceRecordID: 17, ReleaseID: 23}
			preview, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			plan := database.ReleasePlan{ID: fmt.Sprintf("%032d", owner), Kind: kind, OwnerID: owner, Version: "v1", PreparationTaskID: prepareID, PreviewJSON: string(preview), PayloadCiphertext: []byte("private-input"), ExpiresAt: time.Now().Add(time.Minute)}
			if err := db.Create(&plan).Error; err != nil {
				t.Fatal(err)
			}
			read := func(version string) ReleaseActivity {
				t.Helper()
				value, err := CurrentReleaseActivity(ctx, db, kind, owner, node, name, version)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			activity := read("v1")
			if activity.Phase != "review" || activity.PlanID != plan.ID || activity.TaskID != prepareID || activity.Request.Operation != "retry" || activity.Request.SourceRecordID != 17 || activity.Request.ReleaseID != 23 {
				t.Fatal("cannot restore the reviewed operation", activity)
			}
			if value := read("v2"); value.TaskID != "" {
				t.Fatal("restored a plan for an old configuration", value)
			}
			active := database.Task{ID: kind + "-active", Type: prefix + "prepare", NodeID: node, Name: name, Status: "pending", Params: params}
			if err := db.Create(&active).Error; err != nil {
				t.Fatal(err)
			}
			if value := read("v1"); value.Phase != "preparing" || value.TaskID != active.ID || value.PlanID != "" {
				t.Fatal("active preparation did not take priority", value)
			}
			if err := db.Model(&active).Updates(map[string]any{"type": prefix + "deploy", "status": "running"}).Error; err != nil {
				t.Fatal(err)
			}
			if value := read("v1"); value.Phase != "running" || value.TaskID != active.ID {
				t.Fatal("active execution was lost", value)
			}
			if err := db.Model(&active).Update("status", "success").Error; err != nil {
				t.Fatal(err)
			}
			for _, updates := range []map[string]any{{"expires_at": time.Now().Add(-time.Minute)}, {"expires_at": time.Now().Add(time.Minute), "task_id": active.ID}} {
				if err := db.Model(&plan).Updates(updates).Error; err != nil {
					t.Fatal(err)
				}
				if value := read("v1"); value.TaskID != "" {
					t.Fatal("restored an expired or consumed plan", value)
				}
			}
			if err := db.Model(&plan).Update("task_id", "").Error; err != nil {
				t.Fatal(err)
			}
			newer := plan
			newer.ID, newer.TaskID, newer.CreatedAt = fmt.Sprintf("%032d", owner+100), active.ID, time.Now().Add(time.Second)
			if err := db.Create(&newer).Error; err != nil {
				t.Fatal(err)
			}
			if value := read("v1"); value.TaskID != "" {
				t.Fatal("confirmation resurrected an older preview", value)
			}
		})
	}
}
