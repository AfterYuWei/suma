package notification

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

func DefaultSubscription(timezone string) Subscription {
	return Subscription{RuleIDs: []string{}, Policy: DeliveryPolicy{Mode: "immediate", Timezone: timezone, DigestHour: 9}}
}

func (s *Service) validateSubscription(ctx context.Context, channelID string, sub *Subscription) error {
	if sub.RuleIDs == nil {
		sub.RuleIDs = []string{}
	}
	p := &sub.Policy
	if p.Mode == "" {
		p.DigestHour = 9
		p.Mode = "immediate"
	}
	if p.Timezone == "" {
		p.Timezone = "UTC"
	}
	if p.Mode != "immediate" && p.Mode != "merge" && p.Mode != "daily" {
		return ErrInvalid
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil || p.Timezone == "Local" || p.DigestHour < 0 || p.DigestHour > 23 {
		return fmt.Errorf("%w: select an IANA timezone and digest hour", ErrInvalid)
	}
	if (p.QuietStart == "") != (p.QuietEnd == "") || p.QuietStart != "" && p.QuietStart == p.QuietEnd {
		return ErrInvalid
	}
	for _, clock := range []string{p.QuietStart, p.QuietEnd} {
		if clock != "" {
			if _, err := time.Parse("15:04", clock); err != nil {
				return ErrInvalid
			}
		}
	}
	if len(p.Template) > 4000 {
		return ErrInvalid
	}
	clean := p.Template
	for _, field := range []string{"title", "message", "node", "resource", "time", "url"} {
		clean = strings.ReplaceAll(clean, "{{"+field+"}}", "")
	}
	if strings.Contains(clean, "{{") || strings.Contains(clean, "}}") {
		return fmt.Errorf("%w: unsupported template field", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, id := range sub.RuleIDs {
		if id == "" || seen[id] {
			return fmt.Errorf("%w: rule is missing or duplicated", ErrInvalid)
		}
		seen[id] = true
		var count int64
		if err := s.db.WithContext(ctx).Model(&database.NotificationRule{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("%w: selected rule does not exist", ErrInvalid)
		}
	}
	if p.FallbackID == "" {
		if p.FallbackChatID != "" {
			return ErrInvalid
		}
		return nil
	}
	if p.FallbackID == channelID {
		return fmt.Errorf("%w: fallback must be a different channel", ErrInvalid)
	}
	fallback, err := s.Channel(ctx, p.FallbackID)
	if err != nil {
		return fmt.Errorf("%w: fallback channel does not exist", ErrInvalid)
	}
	if fallback.Provider == "feishu_app" && !hasTarget(fallback.Config.Targets, p.FallbackChatID) {
		return fmt.Errorf("%w: select a configured fallback recipient", ErrInvalid)
	}
	if fallback.Provider == "telegram" && fallback.Config.ChatID == "" {
		return fmt.Errorf("%w: configure the fallback recipient", ErrInvalid)
	}
	if fallback.Provider != "feishu_app" {
		p.FallbackChatID = ""
	}
	return nil
}

func removeRule(sub *Subscription, id string) bool {
	ids := make([]string, 0, len(sub.RuleIDs))
	changed := false
	for _, value := range sub.RuleIDs {
		if value == id {
			changed = true
		} else {
			ids = append(ids, value)
		}
	}
	if changed {
		sub.RuleIDs = ids
	}
	return changed
}

func clearFallback(policy *DeliveryPolicy, id string) bool {
	if policy.FallbackID != id {
		return false
	}
	policy.FallbackID, policy.FallbackChatID = "", ""
	return true
}

// A deleted fallback or removed fallback chat cannot leave unsaveable bindings
// in another channel. Existing deliveries retain their immutable fallback snapshot.
func detachFallbacks(tx *gorm.DB, id string, validTargets []Target) error {
	var channels []database.NotificationChannel
	if err := tx.Find(&channels).Error; err != nil {
		return err
	}
	for _, raw := range channels {
		c := decodeChannel(raw)
		clear := func(policy *DeliveryPolicy) bool {
			if policy.FallbackID == id && !hasTarget(validTargets, policy.FallbackChatID) {
				return clearFallback(policy, id)
			}
			return false
		}
		changed := clear(&c.Config.Subscription.Policy)
		for i := range c.Config.Targets {
			if sub := c.Config.Targets[i].SubscriptionOverride; sub != nil {
				changed = clear(&sub.Policy) || changed
			}
		}
		if changed {
			if err := tx.Model(&raw).Updates(map[string]any{"config_json": encode(c.Config), "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
