package taskcenter

import (
	"context"
	"fmt"
	"time"

	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

// BackfillBusinessRules converts persisted business configuration once, before
// the only dispatch loop starts. Failed conversion never leaves a partial cutover.
func (s *Service) BackfillBusinessRules(ctx context.Context, converters ...func(context.Context, *gorm.DB) error) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_database() || ':' || current_schema(), 0))").Error; err != nil {
			return err
		}
		const marker = "0012_task_center_unification_business"
		var count int64
		if err := tx.Model(&database.SchemaMigration{}).Where("key = ?", marker).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		// Recovery may already have projected an interrupted business rule. A
		// retry must agree with that configuration, rather than choose silently
		// between two persisted sources. Compare only configuration, not cadence.
		type projection struct {
			ID                                    uint
			Kind, NodeID, Subject, Params         string
			ScheduleType, Cron, Timezone          string
			IntervalSeconds                       int
			Enabled, TimerEnabled, WebhookEnabled bool
		}
		var owned []string
		for _, kind := range s.kinds.List() {
			if kind.BusinessOwned {
				owned = append(owned, kind.Type)
			}
		}
		const columns = "id,kind,node_id,subject,params::text AS params,schedule_type,cron,timezone,interval_seconds,enabled,timer_enabled,webhook_enabled"
		var before []projection
		if len(owned) > 0 {
			if err := tx.Model(&database.TaskCenterSchedule{}).Select(columns).Where("kind IN ? AND archived_at IS NULL", owned).Find(&before).Error; err != nil {
				return err
			}
		}
		for _, convert := range converters {
			if err := convert(ctx, tx); err != nil {
				return err
			}
		}
		if len(before) > 0 {
			ids := make([]uint, len(before))
			for i, row := range before {
				ids[i] = row.ID
			}
			var after []projection
			if err := tx.Model(&database.TaskCenterSchedule{}).Select(columns).Where("id IN ? AND archived_at IS NULL", ids).Find(&after).Error; err != nil {
				return err
			}
			byID := make(map[uint]projection, len(after))
			for _, row := range after {
				byID[row.ID] = row
			}
			for _, row := range before {
				if current, ok := byID[row.ID]; !ok || current != row {
					return fmt.Errorf("automation rule %d conflicts with its business configuration; review before retrying", row.ID)
				}
			}
		}
		return tx.Create(&database.SchemaMigration{Key: marker, AppliedAt: s.now()}).Error
	})
}
