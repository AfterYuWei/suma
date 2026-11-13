package projectcd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
)

func (s *Service) SetTaskCenter(center *taskcenter.Service) { s.automation = center }

func (s *Service) syncAutomation(tx *gorm.DB, row database.ProjectCD) error {
	if s.automation == nil {
		return nil
	}
	if !row.Enabled {
		return s.automation.ArchiveOwned(tx, "project.cd.check", row.NodeID, row.ProjectName)
	}
	params, err := json.Marshal(map[string]string{"project": row.ProjectName})
	if err != nil {
		return err
	}
	_, err = s.automation.PutOwned(tx, taskcenter.ScheduleInput{Name: "Check " + row.ProjectName, Kind: "project.cd.check", NodeID: row.NodeID, Subject: row.ProjectName, Params: params, ScheduleType: "interval", IntervalSeconds: row.SyncIntervalSeconds, Timezone: "UTC", Enabled: &row.AutomationEnabled, WebhookEnabled: row.WebhookEnabled})
	return err
}

func (s *Service) AutomationKind() taskcenter.KindSpec {
	return taskcenter.KindSpec{
		Type: "project.cd.check", RequiresNode: true, ScheduleTypes: []string{"interval"}, Actions: []string{"check"}, Webhook: true, BusinessOwned: true,
		ValidateParams: func(raw json.RawMessage) error {
			var value struct {
				Project string `json:"project"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&value) != nil || value.Project == "" || len(value.Project) > 128 {
				return taskcenter.ErrInvalidParams
			}
			return nil
		},
		Subject: func(raw json.RawMessage) string {
			var value struct {
				Project string `json:"project"`
			}
			_ = json.Unmarshal(raw, &value)
			return value.Project
		},
		Describe: func(raw json.RawMessage) string {
			var value struct {
				Project string `json:"project"`
			}
			_ = json.Unmarshal(raw, &value)
			return value.Project
		},
		ConfigVersion: func(ctx context.Context, tx *gorm.DB, rule database.TaskCenterSchedule) (string, error) {
			var row database.ProjectCD
			if err := tx.WithContext(ctx).Where("node_id = ? AND project_name = ? AND enabled", rule.NodeID, rule.Subject).First(&row).Error; err != nil {
				return "", err
			}
			return fmt.Sprint(row.Revision), nil
		},
		Change: func(ctx context.Context, tx *gorm.DB, old, next database.TaskCenterSchedule, _ taskcenter.Actor) error {
			var params struct {
				Project string `json:"project"`
			}
			if old.ID == 0 || json.Unmarshal(next.Params, &params) != nil || params.Project != next.Subject {
				return taskcenter.ErrBusinessOwned
			}
			updated := tx.WithContext(ctx).Model(&database.ProjectCD{}).Where("node_id = ? AND project_name = ? AND enabled", next.NodeID, next.Subject).Updates(map[string]any{"automation_enabled": next.Enabled, "sync_interval_seconds": next.IntervalSeconds, "revision": gorm.Expr("revision + 1")})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return taskcenter.ErrBusinessOwned
			}
			return nil
		},
		CanArchive: func(ctx context.Context, tx *gorm.DB, rule database.TaskCenterSchedule) error {
			var count int64
			if err := tx.WithContext(ctx).Model(&database.ProjectCD{}).Where("node_id = ? AND project_name = ? AND enabled", rule.NodeID, rule.Subject).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return taskcenter.ErrBusinessOwned
			}
			return nil
		},
		Start: func(ctx context.Context, request taskcenter.StartRequest) (string, error) {
			var params struct {
				Project string `json:"project"`
			}
			if json.Unmarshal(request.Params, &params) != nil {
				return "", taskcenter.ErrInvalidParams
			}
			if request.ScheduleID != nil && params.Project != request.Subject {
				return "", taskcenter.ErrInvalidParams
			}
			trigger := "manual"
			if request.Reason == taskcenter.ReasonScheduled {
				trigger = "poll"
			} else if request.Reason == taskcenter.ReasonWebhook {
				trigger = "webhook"
			}
			row, err := s.Check(ctx, request.NodeID, params.Project, trigger, Actor{UserID: request.Actor.ID, Name: request.Actor.Name, IP: request.Actor.IP, TriggerRunID: request.RunID, CheckOnly: request.Reason == taskcenter.ReasonManual})
			return row.ID, err
		},
	}
}

func (s *Service) BackfillAutomation(ctx context.Context, tx *gorm.DB) error {
	var rows []database.ProjectCD
	if err := tx.WithContext(ctx).Where("enabled").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.syncAutomation(tx, row); err != nil {
			return err
		}
		var next any
		if row.AutomationEnabled {
			at := time.Now().UTC()
			if row.LastCheckAt != nil {
				candidate := row.LastCheckAt.Add(time.Duration(row.SyncIntervalSeconds) * time.Second)
				if candidate.After(at) {
					at = candidate
				}
			}
			next = at
		}
		if err := tx.Model(&database.TaskCenterSchedule{}).Where("kind = ? AND node_id = ? AND subject = ? AND archived_at IS NULL", "project.cd.check", row.NodeID, row.ProjectName).Update("next_run_at", next).Error; err != nil {
			return err
		}
	}
	return nil
}
