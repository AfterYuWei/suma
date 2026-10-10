package notification

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/suma/suma/server/internal/testutil"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/secret"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type recorder struct {
	mu   sync.Mutex
	sent []Message
	fail map[string]bool
}

func (r *recorder) Send(_ context.Context, c Channel, _ Secrets, m Message) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail[c.ID] {
		return "", errors.New("delivery unavailable")
	}
	r.sent = append(r.sent, m)
	return "provider-id", nil
}
func fixture(t *testing.T) (*Service, *gorm.DB, *recorder, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	db.Logger = logger.Default.LogMode(logger.Silent)
	store, err := secret.Open(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 14, 0, 0, 0, time.UTC)
	sender := &recorder{fail: map[string]bool{}}
	service := NewService(db, store, Dependencies{Sender: sender, Now: func() time.Time { return now }})
	return service, db, sender, &now
}
func channel(t *testing.T, s *Service) Channel {
	t.Helper()
	c, err := s.SaveChannel(context.Background(), "", ChannelInput{Name: "test", Provider: "webhook", Enabled: true, Config: Config{Endpoint: "https://example.com/hook/secret-address", Language: "en-US", Timezone: "Asia/Shanghai"}, Secrets: &Secrets{Authorization: "Bearer private-value", SigningKey: "private-signature"}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func rule(t *testing.T, s *Service, c Channel, mode string) Rule {
	t.Helper()
	r, err := s.SaveRule(context.Background(), "", RuleInput{Name: "test", Enabled: true, Config: RuleConfig{Events: []string{"container.oom", "image.available", "cd.awaiting_approval", "node.recovered"}, ChannelIDs: []string{c.ID}, Mode: mode, Timezone: "Asia/Shanghai", DigestHour: 9, Recovery: true}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestSecretsDedupDurableDeliveryAndUnread(t *testing.T) {
	s, db, sender, now := fixture(t)
	c := channel(t, s)
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "secret-address") || strings.Contains(string(raw), "private-value") {
		t.Fatal("secrets returned")
	}
	var stored database.NotificationChannel
	db.First(&stored, "id = ?", c.ID)
	if strings.Contains(stored.ConfigJSON, "secret-address") || strings.Contains(string(stored.SecretCiphertext), "private") {
		t.Fatal("secrets stored in plaintext")
	}
	rule(t, s, c, "immediate")
	e := event.Event{Type: "container.oom", NodeID: "local", ResourceID: "container", Message: "PASSWORD=private-password", DedupeKey: "oom"}
	if err := s.Ingest(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	s.Ingest(context.Background(), e)
	inbox, _ := s.Inbox(context.Background(), 1)
	if len(inbox.Items) != 1 || strings.Contains(inbox.Items[0].Message, "private-password") {
		t.Fatalf("inbox: %+v", inbox)
	}
	s.Read(context.Background(), 1, inbox.Items[0].ID)
	inbox, _ = s.Inbox(context.Background(), 1)
	if inbox.Unread != 0 {
		t.Fatal("read state not persisted")
	}
	// New service instance drains the same PostgreSQL outbox after a restart.
	recovered := NewService(db, s.secrets, Dependencies{Sender: sender, Now: func() time.Time { return *now }})
	if err := recovered.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent: %d", len(sender.sent))
	}
	deliveries, _ := s.Deliveries(context.Background())
	if deliveries[0].Status != "sent" {
		t.Fatal(deliveries)
	}
	recovered.Tick(context.Background())
	if len(sender.sent) != 1 {
		t.Fatal("sent row replayed")
	}
}
func TestQuietUrgencyMergeAndDailyTimezone(t *testing.T) {
	s, _, sender, now := fixture(t)
	c := channel(t, s)
	r := rule(t, s, c, "merge")
	r.Config.QuietStart = "21:00"
	r.Config.QuietEnd = "08:00"
	r, err := s.SaveRule(context.Background(), r.ID, RuleInput{Name: r.Name, Enabled: true, Version: r.Version, Config: r.Config})
	if err != nil {
		t.Fatal(err)
	}
	s.Ingest(context.Background(), event.Event{Type: "container.oom", ResourceID: "a", Message: "oom"})
	s.Ingest(context.Background(), event.Event{Type: "container.oom", ResourceID: "b", Message: "oom"})
	rows, _ := s.Deliveries(context.Background())
	if len(rows) != 1 || rows[0].DueAt.In(time.FixedZone("CST", 8*3600)).Hour() != 8 {
		t.Fatal(rows)
	}
	s.Ingest(context.Background(), event.Event{Type: "cd.awaiting_approval", Message: "review"})
	s.Tick(context.Background())
	if len(sender.sent) != 1 || len(sender.sent[0].Events) != 1 || sender.sent[0].Events[0].Type != "cd.awaiting_approval" {
		t.Fatal("approval did not bypass quiet period")
	}
	*now = now.Add(11 * time.Hour)
	s.Tick(context.Background())
	if len(sender.sent) != 2 {
		t.Fatal("merged notification missing")
	}
	r.Config.Mode = "daily"
	r.Config.QuietStart = ""
	r.Config.QuietEnd = ""
	s.SaveRule(context.Background(), r.ID, RuleInput{Name: r.Name, Enabled: true, Version: r.Version, Config: r.Config})
	s.Ingest(context.Background(), event.Event{Type: "container.oom", ResourceID: "daily"})
	rows, _ = s.Deliveries(context.Background())
	found := false
	for _, row := range rows {
		if row.Status == "pending" {
			if row.DueAt.In(time.FixedZone("CST", 8*3600)).Hour() != 9 {
				t.Fatal("digest did not use saved timezone")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("digest not queued")
	}
}
func TestRetryFallbackAndSuppression(t *testing.T) {
	s, db, sender, now := fixture(t)
	first := channel(t, s)
	fallback := channel(t, s)
	sender.fail[first.ID] = true
	r := rule(t, s, first, "immediate")
	r.Config.FallbackID = fallback.ID
	s.SaveRule(context.Background(), r.ID, RuleInput{Name: r.Name, Enabled: true, Version: r.Version, Config: r.Config})
	s.Ingest(context.Background(), event.Event{Type: "container.oom", ResourceID: "a"})
	for i := 0; i < 6; i++ {
		s.Tick(context.Background())
		*now = now.Add(time.Hour)
	}
	rows, _ := s.Deliveries(context.Background())
	failed, sent := false, false
	for _, row := range rows {
		failed = failed || row.Status == "failed"
		sent = sent || row.Status == "sent"
	}
	if !failed || !sent {
		t.Fatal(rows)
	}
	var count int64
	db.Model(&database.NotificationEvent{}).Where("type = ?", "notification.failed").Count(&count)
	if count != 1 {
		t.Fatalf("recursive failure events: %d", count)
	}
}
