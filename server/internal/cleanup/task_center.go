package cleanup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
)

func (s *Service) SetTaskCenter(center *taskcenter.Service) { s.automation = center }

func (s *Service) syncAutomation(tx *gorm.DB, p Policy) error {
	if s.automation == nil {
		return nil
	}
	weekday := "*"
	if p.Schedule.Frequency == "weekly" {
		weekday = strconv.Itoa(p.Schedule.Weekday)
	}
	cron := fmt.Sprintf("%d %d * * %s", p.Schedule.Minute, p.Schedule.Hour, weekday)
	_, err := s.automation.PutOwned(tx, taskcenter.ScheduleInput{Name: "Storage cleanup " + p.NodeID, Kind: "system.cleanup", NodeID: p.NodeID, Params: json.RawMessage("{}"), Cron: cron, Timezone: p.Schedule.Timezone, ScheduleType: "cron", Enabled: &p.Enabled, TimerEnabled: &p.Enabled})
	return err
}

func ruleSchedule(rule database.TaskCenterSchedule) (Schedule, error) {
	parts := strings.Fields(rule.Cron)
	if len(parts) != 5 || parts[2] != "*" || parts[3] != "*" {
		return Schedule{}, ErrInvalid
	}
	minute, err := strconv.Atoi(parts[0])
	if err != nil {
		return Schedule{}, ErrInvalid
	}
	hour, err := strconv.Atoi(parts[1])
	if err != nil {
		return Schedule{}, ErrInvalid
	}
	value := Schedule{Frequency: "daily", Hour: hour, Minute: minute, Timezone: rule.Timezone}
	if parts[4] != "*" {
		day, err := strconv.Atoi(parts[4])
		if err != nil || day < 0 || day > 6 {
			return Schedule{}, ErrInvalid
		}
		value.Frequency, value.Weekday = "weekly", day
	}
	config := DefaultConfig(rule.Timezone)
	config.Schedule = value
	if err := Validate(config); err != nil {
		return Schedule{}, err
	}
	return value, nil
}

func (s *Service) AutomationKind() taskcenter.KindSpec {
	return taskcenter.KindSpec{
		Type: "system.cleanup", RequiresNode: true, ScheduleTypes: []string{"cron"}, Actions: []string{"run"}, BusinessOwned: true,
		ValidateParams: func(raw json.RawMessage) error {
			var params struct {
				PreviewID string `json:"preview_id"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&params) != nil {
				return taskcenter.ErrInvalidParams
			}
			return nil
		},
		Describe: func(json.RawMessage) string { return "Docker storage cleanup" },
		ConfigVersion: func(ctx context.Context, tx *gorm.DB, rule database.TaskCenterSchedule) (string, error) {
			var row database.CleanupPolicy
			if err := tx.WithContext(ctx).First(&row, "node_id = ?", rule.NodeID).Error; err != nil {
				return "", err
			}
			return fmt.Sprint(row.Version), nil
		},
		NextRuns: func(rule database.TaskCenterSchedule, after time.Time, count int) ([]time.Time, error) {
			schedule, err := ruleSchedule(rule)
			if err != nil {
				return nil, err
			}
			return NextOccurrences(schedule, after, count), nil
		},
		Advance: func(ctx context.Context, tx *gorm.DB, rule database.TaskCenterSchedule, next *time.Time) error {
			var value any
			if next != nil {
				value = *next
			}
			return tx.WithContext(ctx).Model(&database.CleanupPolicy{}).Where("node_id = ?", rule.NodeID).Update("next_run_at", value).Error
		},
		Change: s.changeAutomation,
		CanArchive: func(ctx context.Context, tx *gorm.DB, rule database.TaskCenterSchedule) error {
			var count int64
			if err := tx.WithContext(ctx).Model(&database.CleanupPolicy{}).Where("node_id = ? AND enabled", rule.NodeID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return taskcenter.ErrBusinessOwned
			}
			return nil
		},
		Start: func(ctx context.Context, request taskcenter.StartRequest) (string, error) {
			var params struct {
				PreviewID string `json:"preview_id"`
			}
			if json.Unmarshal(request.Params, &params) != nil {
				return "", taskcenter.ErrInvalidParams
			}
			actor := Actor{UserID: request.Actor.ID, IP: request.Actor.IP, TriggerRunID: request.RunID}
			if request.Reason == taskcenter.ReasonManual {
				row, err := s.StartRun(ctx, request.NodeID, params.PreviewID, request.Actor.ConfirmationName, actor)
				return row.ID, err
			}
			p, err := s.policy(ctx, request.NodeID)
			if err != nil {
				return "", err
			}
			if !p.Enabled || p.AuthorizedAt == nil || p.AuthorizedBy == nil || fmt.Sprint(p.Version) != request.ConfigVersion {
				return "", ErrConfirmation
			}
			node, err := s.deps.Node(ctx, request.NodeID)
			if err != nil {
				return "", err
			}
			scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			preview, err := s.inventory(scanCtx, request.NodeID, p)
			if err != nil {
				return "", err
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			latest, err := s.policy(ctx, request.NodeID)
			if err != nil {
				return "", err
			}
			if latest.Version != p.Version || !latest.Enabled {
				return "", ErrConflict
			}
			row, err := s.startLocked(ctx, node, preview, actor, "scheduled", request.ScheduledFor, "")
			return row.ID, err
		},
	}
}

func (s *Service) changeAutomation(ctx context.Context, tx *gorm.DB, old, next database.TaskCenterSchedule, actor taskcenter.Actor) error {
	if old.ID == 0 || strings.TrimSpace(string(next.Params)) != "{}" {
		return taskcenter.ErrBusinessOwned
	}
	var row database.CleanupPolicy
	if err := tx.WithContext(ctx).First(&row, "node_id = ?", next.NodeID).Error; err != nil {
		return err
	}
	var before Config
	if err := json.Unmarshal([]byte(row.ConfigJSON), &before); err != nil {
		return err
	}
	after := before
	schedule, err := ruleSchedule(next)
	if err != nil {
		return err
	}
	after.Schedule, after.Enabled = schedule, next.Enabled && next.TimerEnabled
	pauseOnly := before.Enabled && !after.Enabled && before.Schedule == after.Schedule
	var active int64
	if err := tx.Model(&database.CleanupRun{}).Where("node_id = ? AND status IN ?", next.NodeID, []string{"pending", "running"}).Count(&active).Error; err != nil {
		return err
	}
	if active > 0 && !pauseOnly {
		return ErrConflict
	}
	if after.Enabled && (!before.Enabled || row.AuthorizedAt == nil || row.AuthorizedBy == nil) {
		node, err := s.deps.Node(ctx, next.NodeID)
		if err != nil {
			return err
		}
		if actor.ID == nil || !actor.Authorize || actor.ConfirmationName != node.Name {
			return ErrConfirmation
		}
		now := s.now().UTC()
		row.AuthorizedBy, row.AuthorizedAt = actor.ID, &now
	}
	var nextTime any
	if next.NextRunAt != nil {
		nextTime = *next.NextRunAt
	}
	updated := tx.Model(&database.CleanupPolicy{}).Where("node_id = ? AND version = ?", row.NodeID, row.Version).Updates(map[string]any{"enabled": after.Enabled, "config_json": encode(after), "version": row.Version + 1, "authorized_by": row.AuthorizedBy, "authorized_at": row.AuthorizedAt, "next_run_at": nextTime})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Service) BackfillAutomation(ctx context.Context, tx *gorm.DB) error {
	var rows []database.CleanupPolicy
	if err := tx.WithContext(ctx).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var config Config
		if err := json.Unmarshal([]byte(row.ConfigJSON), &config); err != nil {
			return err
		}
		if err := Validate(config); err != nil {
			return err
		}
		p := Policy{Config: config, NodeID: row.NodeID, Version: row.Version, AuthorizedAt: row.AuthorizedAt, AuthorizedBy: row.AuthorizedBy, NextRunAt: row.NextRunAt}
		if err := s.syncAutomation(tx, p); err != nil {
			return err
		}
		updates := map[string]any{"next_run_at": nil}
		if row.NextRunAt != nil && row.Enabled && row.AuthorizedAt != nil && row.AuthorizedBy != nil {
			updates["next_run_at"] = *row.NextRunAt
		}
		if row.Enabled && (row.AuthorizedAt == nil || row.AuthorizedBy == nil) {
			updates["last_error"] = "Cleanup authorization is missing; explicit authorization is required"
		}
		if err := tx.Model(&database.TaskCenterSchedule{}).Where("kind = ? AND node_id = ? AND archived_at IS NULL", "system.cleanup", row.NodeID).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}
