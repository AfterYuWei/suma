package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
)

func feishuChannel(t *testing.T, s *Service, app string, targets ...string) Channel {
	t.Helper()
	config := Config{AppID: app, Timezone: "UTC", Language: "en-US"}
	for _, id := range targets {
		config.Targets = append(config.Targets, Target{ChatID: id, Name: id})
	}
	c, err := s.SaveChannel(context.Background(), "", ChannelInput{Name: app, Provider: "feishu_app", Enabled: true, Config: config, Secrets: &Secrets{Token: "private-app-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func feishuRule(t *testing.T, s *Service, c Channel, mode string, recipients ...string) Rule {
	t.Helper()
	r, err := s.SaveRule(context.Background(), "", RuleInput{Name: "incident", Enabled: true, Config: RuleConfig{Events: []string{"container.oom"}, ChannelIDs: []string{c.ID}, ChannelTargets: map[string][]string{c.ID: recipients}, Mode: mode, Timezone: "UTC", Recovery: true}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestFeishuNotificationOnlyDiscoversWithoutChatOrBinding(t *testing.T) {
	s, db, sender, _ := fixture(t)
	c := feishuChannel(t, s, "cli_discover")
	called := false
	s.SetChatHandler(func(Incoming, database.NotificationBinding) { called = true })
	for _, in := range []Incoming{
		{ID: "one", ChatID: "oc_private", Private: true, Text: "/bind arbitrary-secret"},
		{ID: "two", ChatID: "oc_group", Text: "diagnose containers"},
		{ID: "three", ChatID: "oc_private", Action: "approve", OperationID: "one-time-secret"},
	} {
		in.ChannelID = c.ID
		in.UserID = "tenant:user"
		in.Name = "operator"
		if err := s.HandleIncoming(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		if err := s.HandleIncoming(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	chats, err := s.Chats(context.Background(), c.ID)
	if err != nil || len(chats) != 2 || called || len(sender.sent) != 0 {
		t.Fatalf("unexpected discovery/chat: %v %v %v", chats, called, err)
	}
	for _, chat := range chats {
		if chat.ChatID == "oc_private" && !chat.Private {
			t.Fatal("callback overwrote private conversation evidence")
		}
	}
	var count int64
	db.Model(&database.NotificationIncoming{}).Count(&count)
	if count != 3 {
		t.Fatal("message deduplication failed", count)
	}
	if _, _, err := s.BeginBinding(context.Background(), 1, c.ID); err == nil {
		t.Fatal("notification-only channel accepted binding", err)
	}
	current, _ := s.Channel(context.Background(), c.ID)
	if len(current.Config.Targets) != 0 {
		t.Fatal("discovery subscribed a recipient")
	}
	c.Enabled = false
	if _, err := s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Version: c.Version, Config: c.Config}); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleIncoming(context.Background(), Incoming{ID: "paused", ChannelID: c.ID, UserID: "u", ChatID: "oc_group"}); !errors.Is(err, ErrBinding) {
		t.Fatal("paused channel accepted message", err)
	}
}

func TestFeishuApplicationUniquenessSecretsAndIdentityChange(t *testing.T) {
	s, db, _, _ := fixture(t)
	c := feishuChannel(t, s, "cli_identity", "oc_a", "oc_b")
	input := ChannelInput{Name: "duplicate", Provider: "feishu_app", Config: c.Config, Secrets: &Secrets{Token: "other-secret"}}
	if _, err := s.SaveChannel(context.Background(), "", input); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate notification-only application accepted", err)
	}
	input.Provider = "feishu_webhook"
	input.Config.Endpoint = "https://open.feishu.cn/open-apis/bot/v2/hook/secret"
	if _, err := s.SaveChannel(context.Background(), "", input); !errors.Is(err, ErrInvalid) {
		t.Fatal("removed provider accepted", err)
	}
	updated, err := s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: c.Version, Config: c.Config, Secrets: &Secrets{}})
	if err != nil {
		t.Fatal(err)
	}
	material, _ := s.material(updated)
	if material.Token != "private-app-secret" {
		t.Fatal("blank edit erased secret")
	}
	raw, _ := json.Marshal(updated)
	if strings.Contains(string(raw), material.Token) {
		t.Fatal("secret returned")
	}
	for _, targets := range [][]Target{{{ChatID: "../escape"}}, {{ChatID: "oc_a"}, {ChatID: "oc_a"}}} {
		cfg := updated.Config
		cfg.Targets = targets
		if _, err := s.SaveChannel(context.Background(), updated.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Version: updated.Version, Config: cfg}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid targets accepted", err)
		}
	}
	feishuRule(t, s, updated, "immediate", "oc_a", "oc_b")
	if err := s.Ingest(context.Background(), event.Event{Type: "container.oom", ID: "pending"}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.NotificationChat{ChannelID: c.ID, ChatID: "oc_a", Name: "original", Private: true})
	db.Create(&database.NotificationBinding{ID: "bound", ChannelID: c.ID, Status: "active", CodeHash: "binding-hash"})
	db.Create(&database.NotificationAction{TokenHash: "action-hash", ChannelID: c.ID, BindingID: "bound"})
	cfg := updated.Config
	cfg.AppID = "cli_replacement"
	changed, err := s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: updated.Version, Config: cfg, Secrets: &Secrets{Token: "replacement-secret"}})
	if err != nil || len(changed.Config.Targets) != 0 {
		t.Fatal("application change retained recipients", changed.Config.Targets, err)
	}
	chats, _ := s.Chats(context.Background(), c.ID)
	if len(chats) != 0 {
		t.Fatal("application change retained discovered chats")
	}
	var binding database.NotificationBinding
	db.First(&binding, "id = ?", "bound")
	if binding.Status != "revoked" {
		t.Fatal("binding retained")
	}
	var count int64
	db.Model(&database.NotificationAction{}).Count(&count)
	if count != 0 {
		t.Fatal("approval retained")
	}
	rows, _ := s.Deliveries(context.Background())
	for _, row := range rows {
		if row.Status != "suppressed" {
			t.Fatal("old application still has pending delivery", row)
		}
	}
}

type targetSender struct {
	mu       sync.Mutex
	calls    map[string]int
	failures map[string]bool
}

func (r *targetSender) Send(_ context.Context, c Channel, _ Secrets, _ Message) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[c.Config.ChatID]++
	if r.failures[c.Config.ChatID] {
		return "", errors.New("recipient unavailable")
	}
	return "receipt-" + c.Config.ChatID, nil
}
func TestFeishuMultiRecipientRetryResendAndFallback(t *testing.T) {
	s, _, _, now := fixture(t)
	sender := &targetSender{calls: map[string]int{}, failures: map[string]bool{"oc_b": true}}
	s.deps.Sender = sender
	c := feishuChannel(t, s, "cli_multi", "oc_a", "oc_b")
	fallback := feishuChannel(t, s, "cli_fallback", "oc_fallback", "oc_unused")
	r := feishuRule(t, s, c, "immediate", "oc_a", "oc_b")
	r.Config.FallbackID = fallback.ID
	r.Config.FallbackChatID = "oc_fallback"
	if _, err := s.SaveRule(context.Background(), r.ID, RuleInput{Name: r.Name, Enabled: true, Version: r.Version, Config: r.Config}); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(context.Background(), event.Event{Type: "container.oom", ID: "event-one"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err := s.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(time.Hour)
	}
	if sender.calls["oc_a"] != 1 || sender.calls["oc_b"] != 5 || sender.calls["oc_fallback"] != 1 || sender.calls["oc_unused"] != 0 {
		t.Fatal("cross-target retry/fallback", sender.calls)
	}
	rows, _ := s.Deliveries(context.Background())
	var failedID string
	for _, row := range rows {
		if row.ChatID == "oc_b" && row.Status == "failed" {
			failedID = row.ID
		}
	}
	if failedID == "" {
		t.Fatal("failed recipient not recorded")
	}
	sender.failures["oc_b"] = false
	if err := s.Resend(context.Background(), failedID); err != nil {
		t.Fatal(err)
	}
	s.Tick(context.Background())
	if sender.calls["oc_a"] != 1 || sender.calls["oc_b"] != 6 {
		t.Fatal("resend repeated successful recipient", sender.calls)
	}
	if err := s.Test(context.Background(), c.ID); !errors.Is(err, ErrInvalid) {
		t.Fatal("test accepted implicit recipient", err)
	}
	if err := s.Test(context.Background(), c.ID, "oc_unrelated"); !errors.Is(err, ErrInvalid) {
		t.Fatal("test accepted unrelated recipient", err)
	}
	if err := s.Test(context.Background(), c.ID, "oc_a"); err != nil {
		t.Fatal(err)
	}
	if sender.calls["oc_a"] != 2 || sender.calls["oc_b"] != 6 {
		t.Fatal("test broadcast", sender.calls)
	}
}
func TestFeishuRecipientDigestRemovalAndRuleRouting(t *testing.T) {
	s, _, sender, now := fixture(t)
	c := feishuChannel(t, s, "cli_digest", "oc_a", "oc_b")
	r := feishuRule(t, s, c, "merge", "oc_a", "oc_b")
	for _, id := range []string{"one", "two"} {
		if err := s.Ingest(context.Background(), event.Event{Type: "container.oom", ID: id, ResourceID: id}); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := s.Deliveries(context.Background())
	if len(rows) != 2 {
		t.Fatal("digest recipients merged together", rows)
	}
	for _, row := range rows {
		var ids []string
		json.Unmarshal([]byte(row.EventIDsJSON), &ids)
		if len(ids) != 2 {
			t.Fatal("digest lost event", row)
		}
	}
	cfg := c.Config
	cfg.Targets = cfg.Targets[:1]
	if _, err := s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: c.Version, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	s.Tick(context.Background())
	if len(sender.sent) != 1 {
		t.Fatal("removed recipient was sent", len(sender.sent))
	}
	if _, err := s.SaveRule(context.Background(), r.ID, RuleInput{Name: r.Name, Enabled: true, Version: r.Version, Config: r.Config}); !errors.Is(err, ErrInvalid) {
		t.Fatal("stale recipient accepted", err)
	}
	if _, err := s.SaveRule(context.Background(), r.ID, RuleInput{Name: r.Name, Enabled: false, Version: r.Version, Config: r.Config}); err != nil {
		t.Fatal("stale rule could not be paused", err)
	}
	rows, _ = s.Deliveries(context.Background())
	for _, row := range rows {
		if row.ChatID == "oc_b" && row.Status != "suppressed" {
			t.Fatal("removed recipient remained queued", row)
		}
	}
	c = feishuChannel(t, s, "cli_route", "oc_security", "oc_operations")
	security := feishuRule(t, s, c, "immediate", "oc_security")
	security.Config.Events = []string{"auth.login"}
	if _, err := s.SaveRule(context.Background(), security.ID, RuleInput{Name: "security", Enabled: true, Version: security.Version, Config: security.Config}); err != nil {
		t.Fatal(err)
	}
	feishuRule(t, s, c, "immediate", "oc_operations")
	s.Ingest(context.Background(), event.Event{Type: "auth.login", ID: "login"})
	s.Ingest(context.Background(), event.Event{Type: "container.oom", ID: "oom"})
	rows, _ = s.Deliveries(context.Background())
	for _, row := range rows {
		if row.ChannelID == c.ID {
			if row.ChatID == "oc_security" && row.EventIDsJSON != `["login"]` || row.ChatID == "oc_operations" && row.EventIDsJSON != `["oom"]` {
				t.Fatal("rules routed to incorrect recipient", row)
			}
		}
	}
}

func TestDeletedFeishuChannelCannotLoseInboxOrHealthyDeliveries(t *testing.T) {
	s, _, _, _ := fixture(t)
	c := feishuChannel(t, s, "cli_deleted", "oc_a")
	feishuRule(t, s, c, "immediate", "oc_a")
	healthy := channel(t, s)
	rule(t, s, healthy, "immediate")
	if err := s.DeleteChannel(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(context.Background(), event.Event{ID: "after-delete", Type: "container.oom"}); err != nil {
		t.Fatal("deleted channel blocked event ingestion", err)
	}
	inbox, err := s.Inbox(context.Background(), 1)
	if err != nil || len(inbox.Items) != 1 {
		t.Fatal("event lost", err, inbox)
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Deliveries(context.Background())
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	for _, row := range rows {
		if row.ChannelID == c.ID && row.Status != "suppressed" || row.ChannelID == healthy.ID && row.Status != "sent" {
			t.Fatal("incorrect deleted/healthy destination", row)
		}
	}
}

func TestFeishuConnectionLifecycleIgnoresOldCallbacks(t *testing.T) {
	s, _, _, _ := fixture(t)
	type running struct {
		ctx      context.Context
		status   func(ConnectionStatus)
		incoming func(Incoming) error
	}
	started := make(chan running, 4)
	s.deps.FeishuConnect = func(ctx context.Context, _ Channel, _ Secrets, status func(ConnectionStatus), incoming func(Incoming) error) {
		status(ConnectionStatus{State: "connected"})
		started <- running{ctx, status, incoming}
		<-ctx.Done()
	}
	c := feishuChannel(t, s, "cli_runtime")
	s.Start()
	defer s.Stop()
	next := func() running {
		t.Helper()
		select {
		case runtime := <-started:
			return runtime
		case <-time.After(3 * time.Second):
			t.Fatal("connection did not start")
			return running{}
		}
	}
	old := next()
	state, _ := s.Connection(context.Background(), c.ID)
	if state.State != "connected" {
		t.Fatal(state)
	}
	c, err := s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: c.Version, Config: c.Config, Secrets: &Secrets{Token: "new-private-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	current := next()
	select {
	case <-old.ctx.Done():
	default:
		t.Fatal("old connection not canceled")
	}
	old.status(ConnectionStatus{State: "error", Error: "old error"})
	state, _ = s.Connection(context.Background(), c.ID)
	if state.State != "connected" {
		t.Fatal("old callback overwrote current runtime", state)
	}
	if err := old.incoming(Incoming{ID: "stale", ChannelID: c.ID, UserID: "u", ChatID: "oc_old"}); err == nil {
		t.Fatal("old runtime discovered a chat")
	}
	current.status(ConnectionStatus{State: "error", Error: "new-private-secret connection rejected"})
	state, _ = s.Connection(context.Background(), c.ID)
	if strings.Contains(state.Error, "new-private-secret") {
		t.Fatal("connection secret leaked")
	}
	current.status(ConnectionStatus{State: "connected"})
	c, err = s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: false, Version: c.Version, Config: c.Config})
	if err != nil {
		t.Fatal(err)
	}
	current.status(ConnectionStatus{State: "connected"})
	state, _ = s.Connection(context.Background(), c.ID)
	if state.State != "stopped" {
		t.Fatal("paused callback restored connection", state)
	}
	c, err = s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: c.Version, Config: c.Config})
	if err != nil {
		t.Fatal(err)
	}
	active := next()
	if err := s.DeleteChannel(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-active.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("delete did not cancel connection")
	}
	s.Stop()
	s.Start()
	s.Stop()
}
