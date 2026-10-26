package notification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"gorm.io/gorm"
)

// Only ongoing conditions with an explicit recovery observation are incidents.
// Failed tasks, releases and pulls are separate operation results, not a fault
// that may be suppressed indefinitely while waiting for an unrelated success.
var incidentRecoveries = map[string][]string{
	"node.recovered":         {"node.offline", "agent.error"},
	"container.recovered":    {"container.unhealthy"},
	"container.running":      {"container.exited", "container.oom", "container.restart_loop", "container.unhealthy"},
	"image.check_recovered":  {"image.check_failed"},
	"cd.recovered":           {"cd.drift"},
	"notification.recovered": {"notification.failed"},
	"tls.recovered":          {"tls.expiring"},
}

func incidentFamily(e event.Event) string {
	var resource []string
	switch e.Type {
	case "node.offline", "agent.error", "node.recovered", "tls.expiring", "tls.recovered":
		if e.NodeID == "" {
			return ""
		}
		resource = []string{"node", e.NodeID}
	case "container.exited", "container.oom", "container.restart_loop", "container.unhealthy", "container.recovered", "container.running":
		if e.ResourceID == "" {
			return ""
		}
		resource = []string{"container", e.NodeID, e.ResourceID}
	case "image.check_failed", "image.check_recovered":
		if e.Title == "" {
			return ""
		}
		// A tag can change local image IDs without ending a registry outage.
		resource = []string{"image", e.NodeID, e.Title}
	case "cd.drift", "cd.recovered":
		if e.ResourceID == "" {
			return ""
		}
		resource = []string{"project", e.NodeID, e.ResourceID}
	case "notification.failed", "notification.recovered":
		if e.ResourceID == "" {
			return ""
		}
		resource = []string{"notification_channel", e.ResourceID, e.DedupeKey}
	default:
		return ""
	}
	return incidentHash(resource)
}

func deliveryIncidentKey(channel Channel, chatID string) string {
	return incidentHash([]string{channel.ID, chatID, encode(channel.DestinationVersion)})
}

func incidentHash(parts []string) string {
	sum := sha256.Sum256([]byte(encode(parts)))
	return hex.EncodeToString(sum[:])
}

// Called under a PostgreSQL advisory transaction lock for the whole resource,
// shared by faults and their recovery, including across Service instances.
func observeIncident(tx *gorm.DB, e *event.Event, row *database.NotificationEvent, family string) (bool, []event.Event, error) {
	if family == "" {
		return false, nil, nil
	}
	if types, recovery := incidentRecoveries[e.Type]; recovery {
		recovered := []event.Event{}
		e.RecoveryOf = nil
		for _, typ := range types {
			key := incidentHash([]string{family, typ})
			var fault database.NotificationEvent
			q := tx.Where("incident_key = ? AND resolved_at IS NULL AND last_occurred_at <= ?", key, e.Time)
			if e.Type == "container.running" {
				q = q.Where("last_occurred_at <= ?", e.Time.Add(-2*time.Minute))
			}
			err := q.First(&fault).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return false, nil, err
			}
			var original event.Event
			if err := json.Unmarshal([]byte(fault.DataJSON), &original); err != nil {
				return false, nil, err
			}
			if err := tx.Model(&fault).Update("resolved_at", e.Time).Error; err != nil {
				return false, nil, err
			}
			e.RecoveryOf = append(e.RecoveryOf, fault.ID)
			recovered = append(recovered, original)
		}
		if e.Type == "container.running" {
			e.Type = "container.recovered"
		}
		return len(recovered) == 0, recovered, nil
	}
	key := incidentHash([]string{family, e.Type})
	var active database.NotificationEvent
	err := tx.Where("incident_key = ? AND resolved_at IS NULL", key).First(&active).Error
	if err == nil {
		if e.Time.Before(active.CreatedAt) {
			return true, nil, nil
		}
		// Keep the first message and its immutable delivery snapshots. Repeated
		// observations update only incident evidence and never unread state.
		err = tx.Model(&active).Updates(map[string]any{
			"occurrences":      gorm.Expr("occurrences + 1"),
			"last_occurred_at": gorm.Expr("GREATEST(last_occurred_at, ?)", e.Time),
		}).Error
		return true, nil, err
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil, err
	}
	// A delayed Docker stream event must not reopen an already recovered fault.
	var newer int64
	if err := tx.Model(&database.NotificationEvent{}).Where("incident_key = ? AND resolved_at > ?", key, e.Time).Count(&newer).Error; err != nil {
		return false, nil, err
	}
	if newer > 0 {
		return true, nil, nil
	}
	row.IncidentKey, row.Occurrences, row.LastOccurredAt = key, 1, &e.Time
	return false, nil, nil
}

// A recovery follows the original fault's current subscription, but only for a
// destination that actually had an automatic delivery for that fault. Explicit
// recovery rules can still subscribe independently. Unsubscribed, removed or
// replaced destinations never acquire a recovery through old delivery evidence.
func receivedFault(tx *gorm.DB, channel Channel, chatID, eventID string) (bool, error) {
	var count int64
	err := tx.Table("notification_delivery_events AS link").
		Joins("JOIN notification_deliveries AS delivery ON delivery.id = link.delivery_id").
		Where("link.event_id = ? AND delivery.channel_id = ? AND delivery.chat_id = ? AND delivery.destination_version = ?", eventID, channel.ID, chatID, channel.DestinationVersion).
		Where("delivery.source IN ? AND delivery.status <> ?", []string{"event", "fallback"}, "suppressed").Count(&count).Error
	return count > 0, err
}
