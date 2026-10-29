package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	c, err := s.SaveChannel(context.Background(), "", ChannelInput{Name: "test", Provider: "webhook", Enabled: true, Config: Config{Endpoint: "https://example.com/hook/secret-address", Language: "en-US", Subscription: DefaultSubscription("Asia/Shanghai")}, Secrets: &Secrets{Authorization: "Bearer private-value", SigningKey: "private-signature"}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func rule(t *testing.T, s *Service, c Channel, mode string) Rule {
	t.Helper()
	r, err := s.SaveRule(context.Background(), "", RuleInput{Name: "test", Config: RuleConfig{Events: []string{"container.oom", "image.available", "cd.awaiting_approval", "node.recovered"}}})
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.Channel(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Config.Subscription = Subscription{RuleIDs: []string{r.ID}, Policy: DeliveryPolicy{Mode: mode, Timezone: "Asia/Shanghai", DigestHour: 9}}
	if _, err := s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: c.Enabled, Version: c.Version, Config: c.Config}); err != nil {
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
func TestReadAllPreservesUsersHistoryAndLaterEvents(t *testing.T) {
	s, db, _, now := fixture(t)
	ctx := context.Background()
	if err := s.ReadAll(ctx, 1); err != nil {
		t.Fatal("empty inbox", err)
	}
	rows := make([]database.NotificationEvent, 205)
	for index := range rows {
		id := fmt.Sprintf("read-all-%03d", index)
		rows[index] = database.NotificationEvent{ID: id, Type: "task.failed", Severity: "error", DataJSON: fmt.Sprintf(`{"id":%q,"type":"task.failed"}`, id), CreatedAt: now.Add(time.Duration(index) * time.Second)}
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for user, id := range map[uint]string{1: rows[0].ID, 2: rows[1].ID} {
		if err := s.Read(ctx, user, id); err != nil {
			t.Fatal(err)
		}
	}
	originalReadAt := *now
	*now = now.Add(time.Hour)
	for range 2 {
		if err := s.ReadAll(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	inbox, err := s.Inbox(ctx, 1)
	if err != nil || inbox.Unread != 0 || len(inbox.Items) != 200 {
		t.Fatal("bulk read did not cover the entire inbox", inbox.Unread, len(inbox.Items), err)
	}
	for _, item := range inbox.Items {
		if !item.Read {
			t.Fatal("visible event remained unread", item.ID)
		}
	}
	var count int64
	if err := db.Model(&database.NotificationRead{}).Where("user_id = ?", 1).Count(&count).Error; err != nil || count != 205 {
		t.Fatal("bulk read was limited to visible events or duplicated reads", count, err)
	}
	var previous database.NotificationRead
	if err := db.First(&previous, "user_id = ? AND event_id = ?", 1, rows[0].ID).Error; err != nil || !previous.ReadAt.Equal(originalReadAt) {
		t.Fatal("existing read timestamp changed", err)
	}
	other, err := s.Inbox(ctx, 2)
	if err != nil || other.Unread != 204 {
		t.Fatal("another user's read state changed", other.Unread, err)
	}
	if err := s.Ingest(ctx, event.Event{ID: "after-read-all", Type: "task.failed", ResourceID: "new-task"}); err != nil {
		t.Fatal(err)
	}
	inbox, err = s.Inbox(ctx, 1)
	if err != nil || inbox.Unread != 1 {
		t.Fatal("later events must remain unread", inbox.Unread, err)
	}
}

func TestQuietUrgencyMergeAndDailyTimezone(t *testing.T) {
	s, _, sender, now := fixture(t)
	c := channel(t, s)
	rule(t, s, c, "merge")
	c, _ = s.Channel(context.Background(), c.ID)
	c.Config.Subscription.Policy.QuietStart = "21:00"
	c.Config.Subscription.Policy.QuietEnd = "08:00"
	c, err := s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: c.Version, Config: c.Config})
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
	c.Config.Subscription.Policy.Mode = "daily"
	c.Config.Subscription.Policy.QuietStart = ""
	c.Config.Subscription.Policy.QuietEnd = ""
	s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: c.Version, Config: c.Config})
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
	rule(t, s, first, "immediate")
	first, _ = s.Channel(context.Background(), first.ID)
	first.Config.Subscription.Policy.FallbackID = fallback.ID
	if _, err := s.SaveChannel(context.Background(), first.ID, ChannelInput{Name: first.Name, Provider: first.Provider, Enabled: true, Version: first.Version, Config: first.Config}); err != nil {
		t.Fatal(err)
	}
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
