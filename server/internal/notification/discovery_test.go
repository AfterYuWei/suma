package notification

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func TestTelegramDiscoveryDefaultsAndExplicitOptIn(t *testing.T) {
	s, _, sender, _ := fixture(t)
	ctx := context.Background()
	c, err := s.SaveChannel(ctx, "", ChannelInput{Name: "Telegram", Provider: "telegram", Enabled: true, Config: Config{Timezone: "UTC", Language: "en-US"}, Secrets: &Secrets{Token: "123:private-token"}})
	if err != nil {
		t.Fatal(err)
	}
	in := Incoming{ID: "message", ChannelID: c.ID, UserID: "42", ChatID: "12345", Name: "Admin", Private: true}
	if c.Config.AutoDiscover || needsConnection(c) || !errors.Is(s.HandleIncoming(ctx, in), ErrInvalid) {
		t.Fatal("Telegram discovery enabled by default")
	}
	c.Config.AutoDiscover = true
	c, err = s.SaveChannel(ctx, c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: c.Version, Config: c.Config})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.HandleIncoming(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	chats, err := s.Chats(ctx, c.ID)
	if err != nil || len(chats) != 1 || c.Config.ChatID != "" || len(sender.sent) != 0 {
		t.Fatal("discovery replied or subscribed", chats, err)
	}
	c.Config.AutoDiscover = false
	if _, err = s.SaveChannel(ctx, c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: c.Version, Config: c.Config}); err != nil {
		t.Fatal(err)
	}
	in.ID = "late"
	if err = s.HandleIncoming(ctx, in); !errors.Is(err, ErrInvalid) {
		t.Fatal("disabled discovery accepted incoming message", err)
	}
}

func TestTelegramPollingStoresMetadataAndCursorWithoutCommandsOrCallbacks(t *testing.T) {
	s, db, sender, _ := fixture(t)
	c, err := s.SaveChannel(context.Background(), "", ChannelInput{Name: "Telegram", Provider: "telegram", Enabled: true, Config: Config{AutoDiscover: true, Language: "en-US", Timezone: "UTC"}, Secrets: &Secrets{Token: "123:private-token"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := 0
	a := NewAdapter()
	a.Client = func(bool) *http.Client {
		return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.HasSuffix(req.URL.Path, "/getWebhookInfo"):
				return response(200, `{"ok":true,"result":{"url":""}}`), nil
			case strings.HasSuffix(req.URL.Path, "/getUpdates"):
				body, _ := io.ReadAll(req.Body)
				var request struct {
					Offset  int64    `json:"offset"`
					Allowed []string `json:"allowed_updates"`
				}
				if err := json.Unmarshal(body, &request); err != nil {
					t.Fatal(err)
				}
				if len(request.Allowed) != 1 || request.Allowed[0] != "message" {
					t.Fatal("polling requested callbacks", string(body))
				}
				updates++
				if updates == 1 {
					return response(200, `{"ok":true,"result":[{"update_id":1,"message":{"from":{"id":42,"first_name":"Admin"},"chat":{"id":100,"type":"private"},"text":"/bind PRIVATE-BIND-CODE"}},{"update_id":1,"message":{"from":{"id":42},"chat":{"id":100,"type":"private"},"text":"duplicate"}},{"update_id":2,"message":{"from":{"id":42,"first_name":"Admin"},"chat":{"id":-200,"type":"group","title":"Operations"},"text":"/approve PRIVATE-APPROVAL-TOKEN"}},{"update_id":3,"callback_query":{"id":"legacy","data":"approve:PRIVATE-CALLBACK-TOKEN"}},{"update_id":4,"message":{"from":{"id":99,"is_bot":true},"chat":{"id":100,"type":"private"}}},{"update_id":5,"message":{"from":{"id":0},"chat":{"id":0,"type":"private"}}}]}`), nil
				}
				if request.Offset != 6 {
					t.Fatal("cursor did not advance past ignored updates", request.Offset)
				}
				cancel()
				return response(200, `{"ok":true,"result":[]}`), nil
			default:
				t.Fatalf("unexpected Telegram operation %s", req.URL.Path)
				return nil, errors.New("unexpected operation")
			}
		})}
	}
	s.telegram(ctx, a, c, Secrets{Token: "123:private-token"}, func(in Incoming) error { return s.HandleIncoming(ctx, in) })
	chats, err := s.Chats(context.Background(), c.ID)
	if err != nil || len(chats) != 2 || len(sender.sent) != 0 {
		t.Fatal("unexpected discovered chats or replies", chats, err)
	}
	for _, chat := range chats {
		if chat.ChatID == "-200" && (chat.Name != "Operations" || chat.Private) {
			t.Fatal("group metadata incorrect", chat)
		}
	}
	var incoming []database.NotificationIncoming
	if err := db.Find(&incoming).Error; err != nil || len(incoming) != 2 {
		t.Fatal("messages did not deduplicate", incoming, err)
	}
	encoded, _ := json.Marshal([]any{chats, incoming})
	if strings.Contains(string(encoded), "PRIVATE-") || strings.Contains(string(encoded), "/bind") || strings.Contains(string(encoded), "/approve") {
		t.Fatal("discovery persisted message content")
	}
	var cursor database.Setting
	if err := db.First(&cursor, "key = ?", "notification.cursor."+hash("123:private-token")).Error; err != nil || cursor.Value != "6" {
		t.Fatal("polling cursor was not persisted", cursor, err)
	}
	for _, model := range []any{&database.Task{}, &database.AuditLog{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("incoming messages triggered an operation", count, err)
		}
	}
}

func TestTelegramExistingWebhookIsPreserved(t *testing.T) {
	s, _, _, _ := fixture(t)
	c, err := s.SaveChannel(context.Background(), "", ChannelInput{Name: "Telegram", Provider: "telegram", Enabled: true, Config: Config{AutoDiscover: true, Language: "en-US", Timezone: "UTC"}, Secrets: &Secrets{Token: "123:private-token"}})
	if err != nil {
		t.Fatal(err)
	}
	a := NewAdapter()
	a.Client = func(bool) *http.Client {
		return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(req.URL.Path, "/getWebhookInfo") {
				t.Fatal("existing webhook was modified or polling started", req.URL.Path)
			}
			return response(200, `{"ok":true,"result":{"url":"https://example.com/private-hook"}}`), nil
		})}
	}
	s.telegram(context.Background(), a, c, Secrets{Token: "123:private-token"}, func(Incoming) error { t.Fatal("webhook channel accepted polling message"); return nil })
	c, err = s.Channel(context.Background(), c.ID)
	if err != nil || !strings.Contains(c.LastError, "existing webhook") || strings.Contains(c.LastError, "private-hook") {
		t.Fatal("webhook conflict not reported safely", c.LastError, err)
	}
}

func TestFeishuLegacyCommandsOnlyDiscoverAndOldGenerationCannotWrite(t *testing.T) {
	s, db, sender, _ := fixture(t)
	c := feishuChannel(t, s, "cli_commands")
	handler := feishuEventHandler(c, func() string { return "ou_bot" }, func(in Incoming) error { return s.HandleIncoming(context.Background(), in) }, func(string) {})
	for i, command := range []string{"/bind PRIVATE-CODE", "/approve PRIVATE-TOKEN", "/reject PRIVATE-TOKEN", "list containers", "restart all containers"} {
		text, _ := json.Marshal(map[string]string{"text": command})
		payload := messagePayload(t, c.Config.AppID, string(rune('a'+i)), "text", "p2p", string(text), true)
		for repeat := 0; repeat < 2; repeat++ {
			if _, err := handler.Do(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
		}
	}
	chats, err := s.Chats(context.Background(), c.ID)
	if err != nil || len(chats) != 5 || len(sender.sent) != 0 {
		t.Fatal("commands produced replies", chats, err)
	}
	var incoming []database.NotificationIncoming
	if err := db.Find(&incoming).Error; err != nil || len(incoming) != 5 {
		t.Fatal("SDK message deduplication failed", incoming, err)
	}
	s.connections[c.ID] = connectionEntry{generation: "new"}
	err = s.handleIncoming(context.Background(), Incoming{ID: "old", ChannelID: c.ID, UserID: "tenant:user", ChatID: "oc_old"}, "old")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("stale connection wrote a conversation", err)
	}
}
