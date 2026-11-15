package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

// ReleaseActivity contains navigation pointers only. The owning task and frozen
// plan remain the sources of execution state and confirmation input.
type ReleaseActivity struct {
	Phase   string               `json:"phase,omitempty"`
	TaskID  string               `json:"task_id,omitempty"`
	PlanID  string               `json:"plan_id,omitempty"`
	Request *PrepareReleaseInput `json:"request,omitempty"`
}

func CurrentReleaseActivity(ctx context.Context, db *gorm.DB, kind string, owner uint, node, name, version string) (ReleaseActivity, error) {
	result := ReleaseActivity{}
	prefix := "cd."
	if kind == "project" {
		prefix = "project.cd."
	}
	var active database.Task
	err := db.WithContext(ctx).Select("id", "type").Where("status IN ? AND node_id = ? AND type LIKE ? AND params->>'project' = ?", []string{"pending", "running"}, node, prefix+"%", name).
		Where("parent_task_id IS NULL OR parent_task_id = ''").Order("created_at DESC, id DESC").First(&active).Error
	if err == nil {
		result.TaskID, result.Phase = active.ID, "running"
		if active.Type == prefix+"prepare" {
			result.Phase = "preparing"
		}
		return result, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	var plan database.ReleasePlan
	err = db.WithContext(ctx).Select("id", "preparation_task_id", "preview_json").Where("kind = ? AND owner_id = ? AND version = ? AND task_id = '' AND expires_at > ?", kind, owner, version, time.Now().UTC()).
		// Confirming or replacing a preview must not resurrect an older draft.
		Where("id = (?)", db.Model(&database.ReleasePlan{}).Select("id").Where("kind = ? AND owner_id = ?", kind, owner).Order("created_at DESC, id DESC").Limit(1)).
		Where("preparation_task_id IN (?)", db.Model(&database.Task{}).Select("id").Where("status = ?", "success")).Order("created_at DESC, id DESC").First(&plan).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var view ReleasePlanView
	if err := json.Unmarshal([]byte(plan.PreviewJSON), &view); err != nil {
		return result, err
	}
	result.Phase, result.TaskID, result.PlanID = "review", plan.PreparationTaskID, plan.ID
	result.Request = &PrepareReleaseInput{Operation: view.Operation, ReleaseID: view.ReleaseID, SourceRecordID: view.SourceRecordID}
	return result, nil
}
