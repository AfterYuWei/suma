package notification

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/outbound"
)

// Real provider acceptance is opt-in and reads a private file, never repository
// credentials or command-line tokens. Only an explicit test chat receives messages.
func TestLiveFeishuApplication(t *testing.T) {
	path := os.Getenv("SUMA_LIVE_FEISHU_CREDENTIALS")
	if path == "" {
		t.Skip("private test credentials file required")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("Feishu test credentials require a private regular file (0600)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read private Feishu test credentials")
	}
	var creds struct {
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
	}
	if json.Unmarshal(raw, &creds) != nil || creds.AppID == "" || creds.AppSecret == "" {
		t.Fatal("invalid test credentials file")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adapter := NewAdapter()
	channel := Channel{NotificationChannel: database.NotificationChannel{Provider: "feishu_app"}, Config: Config{AppID: creds.AppID, Language: "en-US", Timezone: "UTC", ChatID: os.Getenv("SUMA_LIVE_FEISHU_CHAT_ID")}}
	material := Secrets{Token: creds.AppSecret}
	if _, err = adapter.Check(ctx, channel, material); err != nil {
		t.Fatal("Feishu credentials/bot check failed", err)
	}
	t.Log("real Feishu credentials and bot capability verified")
	ready := make(chan struct{})
	var once sync.Once
	client := larkws.NewClient(creds.AppID, creds.AppSecret, larkws.WithLogger(quietSDKLogger{}), larkws.WithLogLevel(larkcore.LogLevelError), larkws.WithHttpClient(outbound.Client(false)), larkws.WithEventHandler(dispatcher.NewEventDispatcher("", "")))
	client.SetOnReady(func() { once.Do(func() { close(ready) }) })
	done := make(chan struct{})
	go func() { defer close(done); _ = client.Start(ctx) }()
	defer func() { cancel(); client.Close(); <-done }()
	select {
	case <-ctx.Done():
		t.Fatal("Feishu long connection did not become ready; check platform event/callback configuration")
	case <-ready:
		t.Log("real Feishu outbound WebSocket connection ready")
	}
	ids := []string{}
	for _, id := range strings.Split(os.Getenv("SUMA_LIVE_FEISHU_CHAT_IDS"), ",") {
		id = strings.TrimSpace(id)
		if id != "" && !contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if channel.Config.ChatID != "" && !contains(ids, channel.Config.ChatID) {
		ids = append(ids, channel.Config.ChatID)
	}
	if len(ids) == 0 {
		t.Log("message/card/identity acceptance pending an explicit dedicated test chat")
		return
	}
	for _, id := range ids {
		if !validChatID(id) {
			t.Fatal("invalid dedicated Feishu test conversation")
		}
		channel.Config.ChatID = id
		if _, err = adapter.Send(ctx, channel, material, Message{Text: "SUMA notification acceptance test. No Docker operation is executed."}); err != nil {
			t.Fatal("real Feishu test card delivery failed", err)
		}
	}
	t.Logf("real Feishu interactive test card delivered to %d explicit conversations; notification-only conversation discovery requires a published application", len(ids))
}
