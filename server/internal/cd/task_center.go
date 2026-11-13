package cd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
)

func (s *Service) SetTaskCenter(center *taskcenter.Service) { s.automation = center }

func (s *Service) syncAutomation(tx *gorm.DB, row database.DeliveryProject) error {
	if s.automation == nil || row.GitCloneURL == "" {
		return nil
	}
	params, err := json.Marshal(map[string]string{"project": row.Name})
	if err != nil {
		return err
	}
	_, err = s.automation.PutOwned(tx, taskcenter.ScheduleInput{Name: "Sync " + row.Name, Kind: "cd.sync", Subject: row.Name, Params: params, ScheduleType: "interval", IntervalSeconds: row.SyncIntervalSeconds, Timezone: "UTC", Enabled: &row.AutomationEnabled, WebhookEnabled: row.WebhookEnabled})
	return err
}

func (s *Service) AutomationKind() taskcenter.KindSpec {
	return taskcenter.KindSpec{
		Type: "cd.sync", ScheduleTypes: []string{"interval"}, Actions: []string{"check"}, Webhook: true, BusinessOwned: true,
		ValidateParams: func(raw json.RawMessage) error {
			var value struct {
				Project string `json:"project"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&value) != nil || !validProjectName.MatchString(value.Project) {
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
		ConfigVersion: func(_ context.Context, _ *gorm.DB, row database.TaskCenterSchedule) (string, error) {
			return fmt.Sprint(row.Version), nil
		},
		Change: func(ctx context.Context, tx *gorm.DB, old, next database.TaskCenterSchedule, _ taskcenter.Actor) error {
			var params struct {
				Project string `json:"project"`
			}
			if old.ID == 0 || json.Unmarshal(next.Params, &params) != nil || params.Project != next.Subject {
				return taskcenter.ErrBusinessOwned
			}
			updated := tx.WithContext(ctx).Model(&database.DeliveryProject{}).Where("name = ? AND git_clone_url <> ''", next.Subject).Updates(map[string]any{"automation_enabled": next.Enabled, "sync_interval_seconds": next.IntervalSeconds})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return taskcenter.ErrBusinessOwned
			}
			return nil
		},
		CanArchive: func(ctx context.Context, tx *gorm.DB, row database.TaskCenterSchedule) error {
			var count int64
			if err := tx.WithContext(ctx).Model(&database.DeliveryProject{}).Where("name = ? AND git_clone_url <> ''", row.Subject).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return taskcenter.ErrBusinessOwned
			}
			return nil
		},
		Start: func(ctx context.Context, request taskcenter.StartRequest) (string, error) {
			var params struct {
				Project string `json:"project"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
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
			row, err := s.Sync(ctx, params.Project, trigger, Actor{UserID: request.Actor.ID, Name: request.Actor.Name, IP: request.Actor.IP, TriggerRunID: request.RunID, CheckOnly: request.Reason == taskcenter.ReasonManual})
			return row.ID, err
		},
	}
}

// BackfillAutomation is run once under the startup migration lock, before any
// trigger can be accepted. Existing schedules must match their business owner.
func (s *Service) BackfillAutomation(ctx context.Context, tx *gorm.DB) error {
	if s.automation == nil {
		return errors.New("task center is unavailable")
	}
	var rows []database.DeliveryProject
	if err := tx.WithContext(ctx).Where("git_clone_url <> ''").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.syncAutomation(tx, row); err != nil {
			return err
		}
		var next any
		if row.AutomationEnabled {
			at := time.Now().UTC()
			if row.LastSyncAt != nil {
				candidate := row.LastSyncAt.Add(time.Duration(row.SyncIntervalSeconds) * time.Second)
				if candidate.After(at) {
					at = candidate
				}
			}
			next = at
		}
		if err := tx.Model(&database.TaskCenterSchedule{}).Where("kind = ? AND subject = ? AND node_id = '' AND archived_at IS NULL", "cd.sync", row.Name).Update("next_run_at", next).Error; err != nil {
			return err
		}
	}
	return nil
}
