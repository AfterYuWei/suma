package containermonitor

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	domain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) RecordInstance(ctx context.Context, n Node, c Inventory, at time.Time) error {
	if !identifier.MatchString(n.ID) || n.EngineID == "" || len(n.EngineID) > 128 || !fullID.MatchString(c.ID) {
		return ErrInput
	}
	name := c.Name
	if name == "" {
		name = c.ID[:12]
	}
	row := database.MonitoringInstance{ID: InstanceID(n.ID, n.EngineID, c.ID), NodeID: n.ID, EngineID: n.EngineID, ContainerID: c.ID, NodeName: redact.Bounded(n.Name, 128), Name: redact.Bounded(name, 128), Image: redact.Bounded(redact.Text(c.Image), 256), Project: redact.Bounded(c.Project, 128), Service: redact.Bounded(c.Service, 128), FirstSeen: at, LastSeen: at}
	assignments := map[string]any{"last_seen": gorm.Expr("GREATEST(monitoring_instances.last_seen, EXCLUDED.last_seen)"), "first_seen": gorm.Expr("LEAST(monitoring_instances.first_seen, EXCLUDED.first_seen)")}
	for _, field := range []string{"node_name", "name", "image", "project", "service"} {
		assignments[field] = gorm.Expr("COALESCE(NULLIF(EXCLUDED." + field + ", ''), monitoring_instances." + field + ")")
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.Assignments(assignments)}).Create(&row).Error
}
func (s *Service) EnsurePartitions(ctx context.Context, at time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout = '500ms'").Error; err != nil {
			return err
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':monitoring_partitions',0))").Error; err != nil {
			return err
		}
		for offset := 0; offset < 3; offset++ {
			day := at.UTC().Truncate(24*time.Hour).AddDate(0, 0, offset)
			name := "monitoring_samples_" + day.Format("20060102")
			sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF monitoring_samples FOR VALUES FROM ('%s') TO ('%s')", name, day.Format(time.RFC3339), day.AddDate(0, 0, 1).Format(time.RFC3339))
			if err := tx.Exec(sql).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Service) WriteSamples(ctx context.Context, rows []database.MonitoringSample) error {
	if len(rows) == 0 {
		return nil
	}
	if err := s.EnsurePartitions(ctx, rows[0].RecordedAt); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&rows, 200).Error; err != nil {
			return err
		}
		ids := make([]string, 0, len(rows))
		latest := rows[0].RecordedAt
		for _, r := range rows {
			ids = append(ids, r.InstanceID)
			if r.RecordedAt.After(latest) {
				latest = r.RecordedAt
			}
		}
		return tx.Model(&database.MonitoringInstance{}).Where("id IN ?", ids).Update("last_seen", gorm.Expr("GREATEST(last_seen, ?)", latest)).Error
	})
}
func (s *Service) RecordGap(ctx context.Context, n Node, instance string, since, until time.Time, reason string) error {
	if until.Before(since) {
		until = since
	}
	return s.db.WithContext(ctx).Create(&database.MonitoringGap{NodeID: n.ID, EngineID: n.EngineID, InstanceID: instance, Since: since, Until: until, Reason: reason}).Error
}
func (s *Service) watchEvents(ctx context.Context, n Node, runtime Runtime, changes chan<- domain.ContainerEvent) {
	var checkpoint database.MonitoringCheckpoint
	since := time.Now().UTC()
	if s.db.WithContext(ctx).Where("node_id = ? AND engine_id = ?", n.ID, n.EngineID).First(&checkpoint).Error == nil {
		since = checkpoint.Since.Add(-time.Second)
		_ = s.RecordGap(ctx, n, "", checkpoint.Since, time.Now().UTC(), "event_reconnect_unverified")
	}
	s.mu.RLock()
	effectiveSince := s.activeSince
	s.mu.RUnlock()
	if since.Before(effectiveSince) {
		since = effectiveSince
	}
	backoff := time.Second
	for ctx.Err() == nil {
		err := runtime.MonitoringEvents(ctx, since, func(e domain.ContainerEvent, meta Inventory) error {
			if e.Time.Before(effectiveSince) || !fullID.MatchString(e.ContainerID) {
				return nil
			}
			meta.ID = e.ContainerID
			var known database.MonitoringInstance
			if s.db.WithContext(ctx).First(&known, "id = ?", InstanceID(n.ID, n.EngineID, e.ContainerID)).Error == nil {
				if meta.Name == "" {
					meta.Name = known.Name
				}
				if meta.Image == "" {
					meta.Image = known.Image
				}
				if meta.Project == "" {
					meta.Project = known.Project
				}
				if meta.Service == "" {
					meta.Service = known.Service
				}
			}
			if excluded(s.Settings(), n, meta) {
				return nil
			}
			if err := s.RecordEvent(ctx, n, e, meta); err != nil {
				return err
			}
			if e.Time.After(since) {
				since = e.Time.Add(-time.Second)
			}
			select {
			case changes <- e:
			default:
			}
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		_ = s.RecordGap(ctx, n, "", time.Now().UTC(), time.Now().UTC().Add(backoff), "event_reconnect_unverified")
		if err != nil {
			s.setError(n.ID, "event stream disconnected; retrying")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
func (s *Service) RecordEvent(ctx context.Context, n Node, e domain.ContainerEvent, meta Inventory) error {
	allowed := map[string]bool{"create": true, "start": true, "stop": true, "die": true, "destroy": true, "restart": true, "pause": true, "unpause": true, "kill": true, "health_status": true, "oom": true, "rename": true, "update": true}
	if !allowed[e.Action] || e.Time.IsZero() || !fullID.MatchString(e.ContainerID) {
		return ErrInput
	}
	meta.ID = e.ContainerID
	if err := s.RecordInstance(ctx, n, meta, time.Now().UTC()); err != nil {
		return err
	}
	id := InstanceID(n.ID, n.EngineID, e.ContainerID)
	row := database.MonitoringEvent{ID: InstanceID(id, e.ID, e.TimeNano+e.Action), InstanceID: id, Time: e.Time, TimeNano: e.TimeNano, Action: e.Action, HealthStatus: redact.Bounded(e.HealthStatus, 32), ExitCode: e.ExitCode, Signal: redact.Bounded(e.Signal, 32)}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "node_id"}, {Name: "engine_id"}}, DoUpdates: clause.Assignments(map[string]any{"since": gorm.Expr("GREATEST(monitoring_checkpoints.since, EXCLUDED.since)")})}).Create(&database.MonitoringCheckpoint{NodeID: n.ID, EngineID: n.EngineID, Since: e.Time}).Error
	})
}
func (s *Service) startCleanup() (string, error) {
	if s.tasks == nil {
		return "", nil
	}
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()
	var row database.MonitoringPolicy
	if err := s.db.First(&row, 1).Error; err != nil {
		return "", err
	}
	if row.CleanupTaskID != "" {
		running, err := s.tasks.Get(context.Background(), row.CleanupTaskID)
		if err == nil && (running.Status == task.StatusPending || running.Status == task.StatusRunning) {
			return row.CleanupTaskID, nil
		}
	}
	// A pending checkpoint survives process restart even though the prior Task is canceled.
	if !row.CleanupPending {
		return "", nil
	}
	started, err := s.tasks.StartControlPlane("monitoring.retention", "Clean expired container monitoring history", func(ctx context.Context, report task.Reporter) error {
		report(10, "Removing expired monitoring history")
		if err := s.Prune(ctx, time.Now().UTC()); err != nil {
			return err
		}
		report(100, "Expired monitoring history removed")
		return s.db.WithContext(ctx).Model(&database.MonitoringPolicy{}).Where("id = 1 AND revision = ?", row.Revision).UpdateColumn("cleanup_pending", false).Error
	})
	if err != nil {
		return "", err
	}
	err = s.db.Model(&database.MonitoringPolicy{}).Where("id = 1").UpdateColumn("cleanup_task_id", started.ID).Error
	return started.ID, err
}
func (s *Service) Prune(ctx context.Context, now time.Time) error {
	s.pruneLock.Lock()
	defer s.pruneLock.Unlock()
	cutoff := now.UTC().AddDate(0, 0, -s.Settings().RetentionDays)
	var names []string
	if err := s.db.WithContext(ctx).Raw("SELECT c.relname FROM pg_inherits i JOIN pg_class p ON p.oid=i.inhparent JOIN pg_class c ON c.oid=i.inhrelid JOIN pg_namespace ns ON ns.oid=p.relnamespace WHERE p.relname='monitoring_samples' AND ns.nspname=current_schema()").Scan(&names).Error; err != nil {
		return err
	}
	for _, name := range names {
		if !partitionName.MatchString(name) {
			continue
		}
		day, err := time.Parse("20060102", strings.TrimPrefix(name, "monitoring_samples_"))
		if err != nil || day.Add(24*time.Hour).After(cutoff) {
			continue
		}
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL lock_timeout = '500ms'").Error; err != nil {
				return err
			}
			return tx.Exec("DROP TABLE IF EXISTS " + name).Error
		}); err != nil {
			return err
		}
	}
	for _, table := range []string{"monitoring_samples", "monitoring_events", "monitoring_gaps"} {
		column := "recorded_at"
		if table == "monitoring_events" {
			column = "time"
		}
		if table == "monitoring_gaps" {
			column = "until"
		}
		for {
			var affected int64
			err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec("SET LOCAL lock_timeout = '500ms'").Error; err != nil {
					return err
				}
				// ctid must be paired with tableoid because samples span partitions.
				result := tx.Exec("DELETE FROM "+table+" WHERE (tableoid,ctid) IN (SELECT tableoid,ctid FROM "+table+" WHERE "+column+" < ? LIMIT 5000)", cutoff)
				affected = result.RowsAffected
				return result.Error
			})
			if err != nil {
				return err
			}
			if affected == 0 {
				break
			}
		}
	}
	return s.db.WithContext(ctx).Exec("DELETE FROM monitoring_instances i WHERE i.last_seen < ? AND NOT EXISTS (SELECT 1 FROM monitoring_samples s WHERE s.instance_id=i.id) AND NOT EXISTS (SELECT 1 FROM monitoring_events e WHERE e.instance_id=i.id)", cutoff).Error
}
func (s *Service) markScheduledCleanup(ctx context.Context) error {
	return s.db.WithContext(ctx).Model(&database.MonitoringPolicy{}).Where("id = 1").UpdateColumn("cleanup_pending", true).Error
}
func (s *Service) Instance(ctx context.Context, id string) (database.MonitoringInstance, error) {
	var row database.MonitoringInstance
	if !fullID.MatchString(id) {
		return row, ErrInput
	}
	return row, s.db.WithContext(ctx).Where("last_seen >= ?", time.Now().UTC().AddDate(0, 0, -s.Settings().RetentionDays)).First(&row, "id = ?", id).Error
}

var partitionName = regexp.MustCompile(`^monitoring_samples_[0-9]{8}$`)
