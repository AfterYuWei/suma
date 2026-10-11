package notification

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func channelTargets(c Channel) []Target {
	if c.Provider == "feishu_app" {
		targets := append([]Target{}, c.Config.Targets...)
		sort.Slice(targets, func(i, j int) bool { return targets[i].ChatID < targets[j].ChatID })
		return targets
	}
	if c.Provider == "telegram" && c.Config.ChatID == "" {
		return nil
	}
	return []Target{{ChatID: c.Config.ChatID}}
}

func destinationValid(c Channel, row database.NotificationDelivery) bool {
	if !c.Enabled || c.DestinationVersion != row.DestinationVersion {
		return false
	}
	if c.Provider == "feishu_app" {
		return hasTarget(c.Config.Targets, row.ChatID)
	}
	if c.Provider == "telegram" {
		return c.Config.ChatID != "" && c.Config.ChatID == row.ChatID
	}
	return row.ChatID == ""
}

func schedule(p DeliveryPolicy, e event.Event, now time.Time) (time.Time, string, string) {
	due, status, reason := now, "pending", ""
	if urgent(e) {
		return due, status, reason
	}
	if p.MutedUntil != nil && now.Before(*p.MutedUntil) {
		return due, "suppressed", "temporarily muted"
	}
	zone, err := time.LoadLocation(p.Timezone)
	if err != nil {
		zone = time.UTC
	}
	local := now.In(zone)
	if p.QuietStart != "" {
		clock := local.Format("15:04")
		quiet := p.QuietStart < p.QuietEnd && clock >= p.QuietStart && clock < p.QuietEnd || p.QuietStart > p.QuietEnd && (clock >= p.QuietStart || clock < p.QuietEnd)
		if quiet {
			end, _ := time.Parse("15:04", p.QuietEnd)
			due = time.Date(local.Year(), local.Month(), local.Day(), end.Hour(), end.Minute(), 0, 0, zone)
			if !due.After(now) {
				due = due.AddDate(0, 0, 1)
			}
			reason = "quiet hours"
		}
	}
	if p.Mode == "merge" {
		due = due.Truncate(5 * time.Minute).Add(5 * time.Minute)
	}
	if p.Mode == "daily" {
		dueLocal := due.In(zone)
		candidate := time.Date(dueLocal.Year(), dueLocal.Month(), dueLocal.Day(), p.DigestHour, 0, 0, 0, zone)
		if !candidate.After(due) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		due = candidate
	}
	return due.UTC(), status, reason
}

func (s *Service) queue(tx *gorm.DB, c Channel, target string, e event.Event, snapshot DeliverySnapshot, evidence []MatchedRule) error {
	var fallbackVersion uint64
	if snapshot.Policy.FallbackID != "" {
		var fallback database.NotificationChannel
		err := tx.First(&fallback, "id = ?", snapshot.Policy.FallbackID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		fallbackVersion = fallback.DestinationVersion
	}
	return s.enqueue(tx, c, target, e, snapshot, evidence, "event", "", fallbackVersion)
}

func (s *Service) enqueue(tx *gorm.DB, c Channel, target string, e event.Event, snapshot DeliverySnapshot, evidence []MatchedRule, source, origin string, fallbackVersion uint64) error {
	// Serialize batch append and event claims across service instances, not just goroutines.
	lockKey := encode([]string{c.ID, target})
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
		return err
	}
	key := hash(encode([]string{e.ID, c.ID, target}))
	var count int64
	if err := tx.Model(&database.NotificationDeliveryEvent{}).Where("dedupe_key = ?", key).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	due, status, reason := schedule(snapshot.Policy, e, s.deps.Now())
	if source == "fallback" {
		due, status, reason = s.deps.Now(), "pending", "fallback delivery"
	}
	part := e.ID
	if source == "fallback" {
		part = origin
	} else if !urgent(e) && snapshot.Policy.Mode != "immediate" && status == "pending" {
		part = due.Format(time.RFC3339Nano)
	}
	batch := hash(encode([]string{c.ID, target, source, part, encode(snapshot), encode(fallbackVersion)}))
	var row database.NotificationDelivery
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("batch_key = ? AND status = ?", batch, "pending").First(&row).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		if err := tx.Model(&database.NotificationDeliveryEvent{}).Where("delivery_id = ?", row.ID).Count(&count).Error; err != nil {
			return err
		}
		if count >= 200 {
			row = database.NotificationDelivery{}
			batch = hash(batch + "|" + e.ID)
		}
	}
	if row.ID == "" {
		row = database.NotificationDelivery{ID: ID(), ChannelID: c.ID, ChatID: target, Source: source, OriginDeliveryID: origin, DestinationVersion: c.DestinationVersion, FallbackDestinationVersion: fallbackVersion, SnapshotJSON: encode(snapshot), BatchKey: batch, Status: status, Reason: reason, DueAt: due}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return tx.Create(&database.NotificationDeliveryEvent{DeliveryID: row.ID, EventID: e.ID, DedupeKey: &key, MatchedRulesJSON: encode(evidence)}).Error
}

func (s *Service) deliveryEvents(ctx context.Context, id string) ([]database.NotificationEvent, []database.NotificationDeliveryEvent, error) {
	links := []database.NotificationDeliveryEvent{}
	if err := s.db.WithContext(ctx).Where("delivery_id = ?", id).Find(&links).Error; err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(links))
	for _, link := range links {
		ids = append(ids, link.EventID)
	}
	events := []database.NotificationEvent{}
	err := s.db.WithContext(ctx).Where("id IN ?", ids).Order("created_at ASC, id ASC").Find(&events).Error
	return events, links, err
}

func (s *Service) Deliveries(ctx context.Context) ([]Delivery, error) {
	rows := []database.NotificationDelivery{}
	if err := s.db.WithContext(ctx).Order("created_at DESC, id DESC").Limit(200).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	links := []database.NotificationDeliveryEvent{}
	if err := s.db.WithContext(ctx).Where("delivery_id IN ?", ids).Find(&links).Error; err != nil {
		return nil, err
	}
	byDelivery := map[string][]database.NotificationDeliveryEvent{}
	for _, link := range links {
		byDelivery[link.DeliveryID] = append(byDelivery[link.DeliveryID], link)
	}
	out := make([]Delivery, 0, len(rows))
	for _, row := range rows {
		evidence := []MatchedRule{}
		seen := map[string]bool{}
		for _, link := range byDelivery[row.ID] {
			var matched []MatchedRule
			if err := json.Unmarshal([]byte(link.MatchedRulesJSON), &matched); err != nil {
				return nil, err
			}
			for _, rule := range matched {
				key := encode(rule)
				if !seen[key] {
					evidence = append(evidence, rule)
					seen[key] = true
				}
			}
		}
		sort.Slice(evidence, func(i, j int) bool {
			if evidence[i].ID == evidence[j].ID {
				return evidence[i].Version < evidence[j].Version
			}
			return evidence[i].ID < evidence[j].ID
		})
		out = append(out, Delivery{NotificationDelivery: row, MatchedRules: evidence, EventCount: len(byDelivery[row.ID])})
	}
	return out, nil
}

func (s *Service) Resend(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row database.NotificationDelivery
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return err
		}
		var raw database.NotificationChannel
		if err := tx.First(&raw, "id = ?", row.ChannelID).Error; err != nil {
			return err
		}
		if !destinationValid(decodeChannel(raw), row) {
			return ErrInvalid
		}
		links := []database.NotificationDeliveryEvent{}
		if err := tx.Where("delivery_id = ?", id).Find(&links).Error; err != nil {
			return err
		}
		row.ID, row.Source, row.OriginDeliveryID = ID(), "manual", id
		row.Status, row.Reason, row.Attempts, row.DueAt = "pending", "manual resend", 0, s.deps.Now()
		row.LeaseUntil, row.ProviderMessageID, row.CreatedAt, row.UpdatedAt = nil, "", time.Time{}, time.Time{}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		for _, link := range links {
			link.DeliveryID, link.DedupeKey = row.ID, nil
			if err := tx.Create(&link).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) queueFallback(tx *gorm.DB, row database.NotificationDelivery, snapshot DeliverySnapshot, links []database.NotificationDeliveryEvent) error {
	var raw database.NotificationChannel
	err := tx.First(&raw, "id = ?", snapshot.Policy.FallbackID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	c := decodeChannel(raw)
	target := snapshot.Policy.FallbackChatID
	if c.Provider == "telegram" {
		target = c.Config.ChatID
	}
	if !destinationValid(c, database.NotificationDelivery{ChatID: target, DestinationVersion: row.FallbackDestinationVersion}) {
		return nil
	}
	snapshot.Policy.FallbackID, snapshot.Policy.FallbackChatID = "", ""
	for _, link := range links {
		var eventRow database.NotificationEvent
		if err := tx.First(&eventRow, "id = ?", link.EventID).Error; err != nil {
			return err
		}
		var e event.Event
		if err := json.Unmarshal([]byte(eventRow.DataJSON), &e); err != nil {
			return err
		}
		var evidence []MatchedRule
		if err := json.Unmarshal([]byte(link.MatchedRulesJSON), &evidence); err != nil {
			return err
		}
		if err := s.enqueue(tx, c, target, e, snapshot, evidence, "fallback", row.ID, 0); err != nil {
			return err
		}
	}
	return nil
}
