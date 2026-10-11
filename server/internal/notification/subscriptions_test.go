package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"gorm.io/gorm"
)

func filterRule(t *testing.T, s *Service, name string, types ...string) Rule {
	t.Helper()
	r, err := s.SaveRule(context.Background(), "", RuleInput{Name: name, Config: RuleConfig{Events: types}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func saveConfig(t *testing.T, s *Service, c Channel) Channel {
	t.Helper()
	current, err := s.Channel(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: c.Enabled, Version: current.Version, Config: c.Config})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
func ingest(t *testing.T, s *Service, id, typ string) {
	t.Helper()
	if err := s.Ingest(context.Background(), event.Event{ID: id, Type: typ, ResourceID: id, Title: "Sample", Message: "Sample event", Severity: "error"}); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionSelectionActivatesRulesWithoutEnableState(t *testing.T) {
	s, db, sender, _ := fixture(t)
	r := filterRule(t, s, "Selected incidents", "container.oom")
	c := channel(t, s)
	if db.Migrator().HasColumn(&database.NotificationRule{}, "enabled") {
		t.Fatal("fresh rules still have an enabled column")
	}
	encoded, err := json.Marshal(r)
	if err != nil || strings.Contains(string(encoded), `"enabled"`) {
		t.Fatal("rule contract still exposes an enabled state", err)
	}
	// Existing databases may retain an unused old column. It must not gate a
	// selected rule or require resetting the user's database.
	if err := db.Exec("ALTER TABLE notification_rules ADD COLUMN enabled boolean NOT NULL DEFAULT false").Error; err != nil {
		t.Fatal(err)
	}
	ingest(t, s, "before-subscription", "container.oom")
	rows, err := s.Deliveries(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatal("unsubscribed rule delivered", rows, err)
	}
	c.Config.Subscription.RuleIDs = []string{r.ID}
	c = saveConfig(t, s, c)
	ingest(t, s, "selected", "container.oom")
	c.Config.Subscription.RuleIDs = nil
	c = saveConfig(t, s, c)
	ingest(t, s, "unsubscribed", "container.oom")
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Deliveries(context.Background())
	if err != nil || len(rows) != 1 || len(sender.sent) != 1 || len(rows[0].MatchedRules) != 1 || rows[0].MatchedRules[0].ID != r.ID {
		t.Fatal("selected rule did not deliver or unsubscription changed queued evidence", rows, err)
	}
	c.Config.Subscription.RuleIDs = []string{r.ID}
	saveConfig(t, s, c)
	ingest(t, s, "selected-again", "container.oom")
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Deliveries(context.Background())
	if err != nil || len(rows) != 2 || len(sender.sent) != 2 {
		t.Fatal("resubscribing did not apply only to new events", rows, err)
	}
}
func TestSubscriptionsUnionInheritanceOverridesAndDiscovery(t *testing.T) {
	s, _, _, _ := fixture(t)
	sender := &targetSender{calls: map[string]int{}, failures: map[string]bool{}}
	s.deps.Sender = sender
	a := filterRule(t, s, "Important", "container.oom")
	b := filterRule(t, s, "Production", "container.oom")
	recovery := filterRule(t, s, "Recovery", "node.recovered")
	c := feishuChannel(t, s, "cli_subscriptions", "oc_default", "oc_custom", "oc_empty")
	c.Config.Subscription.RuleIDs = []string{a.ID, b.ID}
	custom := DefaultSubscription("UTC")
	custom.RuleIDs = []string{recovery.ID}
	empty := DefaultSubscription("UTC")
	c.Config.Targets[1].SubscriptionOverride = &custom
	c.Config.Targets[2].SubscriptionOverride = &empty
	c = saveConfig(t, s, c)
	ingest(t, s, "oom", "container.oom")
	ingest(t, s, "recovered", "node.recovered")
	if err := s.HandleIncoming(context.Background(), Incoming{ID: "discover", ChannelID: c.ID, UserID: "human", ChatID: "oc_discovered", Name: "New group"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Deliveries(context.Background())
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	for _, row := range rows {
		if row.ChatID == "oc_default" && len(row.MatchedRules) != 2 || row.ChatID == "oc_custom" && len(row.MatchedRules) != 1 {
			t.Fatal("matching evidence", row)
		}
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sender.calls["oc_default"] != 1 || sender.calls["oc_custom"] != 1 || sender.calls["oc_empty"] != 0 || sender.calls["oc_discovered"] != 0 {
		t.Fatal(sender.calls)
	}
	// Restoring inheritance and explicitly selecting a discovered target apply to new events only.
	c.Config.Targets[1].SubscriptionOverride = nil
	c.Config.Targets = append(c.Config.Targets, Target{ChatID: "oc_discovered", Name: "New group"})
	saveConfig(t, s, c)
	ingest(t, s, "new-oom", "container.oom")
	s.Tick(context.Background())
	if sender.calls["oc_default"] != 2 || sender.calls["oc_custom"] != 2 || sender.calls["oc_discovered"] != 1 {
		t.Fatal(sender.calls)
	}
}

func TestQueuedSnapshotSurvivesRuleAndSubscriptionChanges(t *testing.T) {
	s, db, sender, now := fixture(t)
	r := filterRule(t, s, "Original name", "container.oom")
	c := channel(t, s)
	c.Config.Subscription.RuleIDs = []string{r.ID}
	c.Config.Subscription.Policy.Mode = "merge"
	c.Config.Subscription.Policy.Template = "OLD {{title}} {{time}}"
	c = saveConfig(t, s, c)
	ingest(t, s, "old-event", "container.oom")
	rows, _ := s.Deliveries(context.Background())
	old := rows[0]
	c.Config.Subscription.Policy.Template = "NEW {{title}}"
	c.Config.Subscription.Policy.Timezone = "UTC"
	c = saveConfig(t, s, c)
	ingest(t, s, "new-event", "container.oom")
	rows, _ = s.Deliveries(context.Background())
	if len(rows) != 2 {
		t.Fatal("policy change joined old batch", rows)
	}
	// Editing and deleting a rule remove future matches/bindings but preserve queued evidence.
	r.Config.Events = []string{"node.offline"}
	if _, err := s.SaveRule(context.Background(), r.ID, RuleInput{Name: "Changed name", Version: r.Version, Config: r.Config}); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, "no-longer-matching", "container.oom")
	version := c.Version
	if err := s.DeleteRule(context.Background(), r.ID); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Channel(context.Background(), c.ID)
	if len(current.Config.Subscription.RuleIDs) != 0 || current.Version != version+1 {
		t.Fatal("rule binding not detached", current)
	}
	// A mute added after queueing cannot change existing due times or contents.
	future := now.Add(48 * time.Hour)
	current.Config.Subscription.Policy.MutedUntil = &future
	saveConfig(t, s, current)
	*now = now.Add(10 * time.Minute)
	restarted := NewService(db, s.secrets, Dependencies{Sender: sender, Now: func() time.Time { return *now }})
	if err := restarted.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 2 {
		t.Fatal("queued snapshot canceled", sender.sent)
	}
	oldFound, newFound := false, false
	for _, m := range sender.sent {
		oldFound = oldFound || strings.Contains(m.Text, "OLD Sample 2026-10-03 22:")
		newFound = newFound || strings.HasPrefix(m.Text, "NEW Sample")
	}
	if !oldFound || !newFound {
		t.Fatal(sender.sent)
	}
	rows, _ = restarted.Deliveries(context.Background())
	for _, row := range rows {
		if row.MatchedRules[0].Name != "Original name" || row.MatchedRules[0].Version != 1 {
			t.Fatal("history changed", row)
		}
	}
	if err := restarted.Resend(context.Background(), old.ID); err != nil {
		t.Fatal(err)
	}
	restarted.Tick(context.Background())
	rows, _ = restarted.Deliveries(context.Background())
	if len(sender.sent) != 3 || len(rows) != 3 || rows[0].Source != "manual" || rows[0].OriginDeliveryID != old.ID {
		t.Fatal("manual resend lost snapshot or history", rows, sender.sent)
	}
}

func TestConcurrentServicesDeduplicateAndClaimDurableBatches(t *testing.T) {
	s, db, sender, now := fixture(t)
	a := filterRule(t, s, "A", "container.oom")
	b := filterRule(t, s, "B", "container.oom")
	c := channel(t, s)
	c.Config.Subscription.RuleIDs = []string{a.ID, b.ID}
	c.Config.Subscription.Policy.Mode = "merge"
	saveConfig(t, s, c)
	second := NewService(db, s.secrets, Dependencies{Sender: sender, Now: func() time.Time { return *now }})
	var wg sync.WaitGroup
	failures := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			service := s
			if i%2 != 0 {
				service = second
			}
			id := fmt.Sprintf("concurrent-%d", i/2)
			failures <- service.Ingest(context.Background(), event.Event{ID: id, Type: "container.oom", ResourceID: id})
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Deliveries(context.Background())
	if err != nil || len(rows) != 1 || rows[0].EventCount != 20 || len(rows[0].MatchedRules) != 2 {
		t.Fatal(rows, err)
	}
	var links []database.NotificationDeliveryEvent
	if err := db.Find(&links).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := links[0]
	duplicate.DeliveryID = "another-delivery"
	if err := db.Create(&duplicate).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatal("database did not enforce target event uniqueness", err)
	}
	*now = now.Add(10 * time.Minute)
	wg.Add(2)
	go func() { defer wg.Done(); s.Tick(context.Background()) }()
	go func() { defer wg.Done(); second.Tick(context.Background()) }()
	wg.Wait()
	if len(sender.sent) != 1 || len(sender.sent[0].Events) != 20 {
		t.Fatal("batch lost events or sent twice", sender.sent)
	}
}

func TestFallbackSharesAutomaticClaimsAndPreservesEvidence(t *testing.T) {
	s, _, sender, now := fixture(t)
	r := filterRule(t, s, "Failures", "container.oom")
	backup := channel(t, s)
	first := channel(t, s)
	second := channel(t, s)
	for _, c := range []Channel{first, second} {
		c.Config.Subscription.RuleIDs = []string{r.ID}
		c.Config.Subscription.Policy.Template = "ORIGINAL {{title}}"
		c.Config.Subscription.Policy.FallbackID = backup.ID
		saveConfig(t, s, c)
		sender.fail[c.ID] = true
	}
	ingest(t, s, "shared-failure", "container.oom")
	for i := 0; i < 6; i++ {
		if err := s.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(time.Hour)
	}
	rows, _ := s.Deliveries(context.Background())
	fallbacks := 0
	for _, row := range rows {
		if row.Source == "fallback" {
			fallbacks++
			if row.ChannelID != backup.ID || row.OriginDeliveryID == "" || len(row.MatchedRules) != 1 {
				t.Fatal(row)
			}
		}
	}
	if fallbacks != 1 || len(sender.sent) != 1 || !strings.HasPrefix(sender.sent[0].Text, "ORIGINAL Sample") {
		t.Fatal("shared fallback duplicated or changed content", rows, sender.sent)
	}
}

func TestSubscriptionsValidateAtomicallyAndTransportPauseCancelsQueue(t *testing.T) {
	s, _, sender, now := fixture(t)
	r := filterRule(t, s, "R", "container.oom")
	c := feishuChannel(t, s, "cli_validation", "oc_a")
	c.Config.Subscription.RuleIDs = []string{r.ID}
	c.Config.Subscription.Policy.Mode = "merge"
	c = saveConfig(t, s, c)
	bad := DefaultSubscription("UTC")
	bad.RuleIDs = []string{"missing"}
	c.Config.Targets[0].SubscriptionOverride = &bad
	_, err := s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: "Should not save", Provider: c.Provider, Enabled: true, Version: c.Version, Config: c.Config})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	current, _ := s.Channel(context.Background(), c.ID)
	if current.Name != c.Name || current.Version != c.Version || current.Config.Targets[0].SubscriptionOverride != nil {
		t.Fatal("partial subscription save", current)
	}
	ingest(t, s, "queued", "container.oom")
	current.Enabled = false
	current = saveConfig(t, s, current)
	current.Enabled = true
	saveConfig(t, s, current)
	*now = now.Add(time.Hour)
	s.Tick(context.Background())
	rows, _ := s.Deliveries(context.Background())
	if len(sender.sent) != 0 || rows[0].Status != "suppressed" {
		t.Fatal("pause did not cancel pending queue", rows)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "snapshot_json") || strings.Contains(string(raw), "private-app-secret") {
		t.Fatal("queue internals leaked")
	}
}

func TestNewMuteDoesNotAffectQueuedEventsAndUrgencyStillBypassesPolicy(t *testing.T) {
	s, _, sender, now := fixture(t)
	r := filterRule(t, s, "Incidents", "container.oom", "cd.awaiting_approval")
	c := channel(t, s)
	c.Config.Subscription.RuleIDs = []string{r.ID}
	c.Config.Subscription.Policy.Mode = "merge"
	c = saveConfig(t, s, c)
	ingest(t, s, "before-mute", "container.oom")
	future := now.Add(24 * time.Hour)
	c.Config.Subscription.Policy.MutedUntil = &future
	c = saveConfig(t, s, c)
	ingest(t, s, "muted", "container.oom")
	if err := s.Ingest(context.Background(), event.Event{ID: "critical", Type: "container.oom", ResourceID: "critical", Severity: "critical"}); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, "approval", "cd.awaiting_approval")
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 2 {
		t.Fatal("urgent events waited for mute", sender.sent)
	}
	*now = now.Add(10 * time.Minute)
	s.Tick(context.Background())
	if len(sender.sent) != 3 {
		t.Fatal("new mute affected old batch", sender.sent)
	}
	rows, _ := s.Deliveries(context.Background())
	skipped := 0
	for _, row := range rows {
		if row.Status == "suppressed" {
			skipped++
		}
	}
	if skipped != 1 {
		t.Fatal(rows)
	}
}

func TestDigestBoundsAndRuleVersionsRetainPerEventEvidence(t *testing.T) {
	s, db, sender, now := fixture(t)
	r := filterRule(t, s, "Original", "container.oom")
	c := channel(t, s)
	c.Config.Subscription.RuleIDs = []string{r.ID}
	c.Config.Subscription.Policy.Mode = "merge"
	saveConfig(t, s, c)
	for i := 0; i < 201; i++ {
		if i == 100 {
			var err error
			r, err = s.SaveRule(context.Background(), r.ID, RuleInput{Name: "Renamed", Version: r.Version, Config: r.Config})
			if err != nil {
				t.Fatal(err)
			}
		}
		ingest(t, s, fmt.Sprintf("bounded-%03d", i), "container.oom")
	}
	rows, _ := s.Deliveries(context.Background())
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	total := 0
	versions := map[uint64]bool{}
	for _, row := range rows {
		if row.EventCount > 200 {
			t.Fatal("batch unbounded", row)
		}
		total += row.EventCount
		for _, r := range row.MatchedRules {
			versions[r.Version] = true
		}
	}
	if total != 201 || len(versions) != 2 {
		t.Fatal("event evidence lost", rows)
	}
	*now = now.Add(10 * time.Minute)
	restarted := NewService(db, s.secrets, Dependencies{Sender: sender, Now: func() time.Time { return *now }})
	restarted.Tick(context.Background())
	count := 0
	for _, m := range sender.sent {
		count += len(m.Events)
	}
	if len(sender.sent) != 2 || count != 201 {
		t.Fatal("digest overflow lost events", sender.sent)
	}
}

func TestDeletingFallbackDetachesCurrentPoliciesAndKeepsPendingSnapshots(t *testing.T) {
	s, _, _, _ := fixture(t)
	r := filterRule(t, s, "R", "container.oom")
	backup := channel(t, s)
	c := feishuChannel(t, s, "cli_detach", "oc_a", "oc_b")
	c.Config.Subscription.RuleIDs = []string{r.ID}
	c.Config.Subscription.Policy.FallbackID = backup.ID
	override := c.Config.Subscription
	c.Config.Targets[1].SubscriptionOverride = &override
	c = saveConfig(t, s, c)
	ingest(t, s, "original", "container.oom")
	if err := s.DeleteChannel(context.Background(), backup.ID); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Channel(context.Background(), c.ID)
	if current.Version != c.Version+1 || current.Config.Subscription.Policy.FallbackID != "" || current.Config.Targets[1].SubscriptionOverride.Policy.FallbackID != "" {
		t.Fatal(current)
	}
	rows, _ := s.Deliveries(context.Background())
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	for _, row := range rows {
		var snapshot DeliverySnapshot
		json.Unmarshal([]byte(row.SnapshotJSON), &snapshot)
		if row.Status != "pending" || snapshot.Policy.FallbackID != backup.ID {
			t.Fatal("old snapshot changed", row)
		}
	}
}

func TestRemovingFallbackRecipientDetachesOnlyInvalidReferences(t *testing.T) {
	s, _, _, _ := fixture(t)
	r := filterRule(t, s, "R", "container.oom")
	backup := feishuChannel(t, s, "cli_backup_removal", "oc_keep", "oc_remove")
	c := channel(t, s)
	c.Config.Subscription.RuleIDs = []string{r.ID}
	c.Config.Subscription.Policy.FallbackID = backup.ID
	c.Config.Subscription.Policy.FallbackChatID = "oc_remove"
	c = saveConfig(t, s, c)
	healthy := channel(t, s)
	healthy.Config.Subscription.RuleIDs = []string{r.ID}
	healthy.Config.Subscription.Policy.FallbackID = backup.ID
	healthy.Config.Subscription.Policy.FallbackChatID = "oc_keep"
	healthy = saveConfig(t, s, healthy)
	backup.Config.Targets = backup.Config.Targets[:1]
	saveConfig(t, s, backup)
	removed, _ := s.Channel(context.Background(), c.ID)
	kept, _ := s.Channel(context.Background(), healthy.ID)
	if removed.Config.Subscription.Policy.FallbackID != "" || removed.Version != c.Version+1 || kept.Config.Subscription.Policy.FallbackChatID != "oc_keep" || kept.Version != healthy.Version {
		t.Fatal("fallback cleanup changed valid target", removed, kept)
	}
}
