package notification

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func messagePayload(t *testing.T, appID, id, kind, chatType, content string, senderTenant bool) []byte {
	t.Helper()
	sender := map[string]any{"sender_type": "user", "sender_id": map[string]any{"open_id": "ou_operator"}}
	if senderTenant {
		sender["tenant_key"] = "tenant_fixture"
	}
	message := map[string]any{"message_id": id, "chat_id": "oc_" + id, "chat_type": chatType, "message_type": kind, "content": content}
	if chatType == "group" {
		message["mentions"] = []any{map[string]any{"key": "@_user_1", "id": map[string]any{"open_id": "ou_bot"}}}
	}
	payload, err := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]any{"event_id": "event_" + id, "event_type": "im.message.receive_v1", "app_id": appID, "tenant_key": "tenant_fixture"}, "event": map[string]any{"sender": sender, "message": message}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestFeishuSDKNonTextMessagesOnlyDiscover(t *testing.T) {
	s, _, sender, _ := fixture(t)
	c := feishuChannel(t, s, "cli_rich")
	handler := feishuEventHandler(c, func() string { return "ou_bot" }, func(in Incoming) error { return s.HandleIncoming(context.Background(), in) }, func(string) {})
	for _, kind := range []string{"post", "image", "file"} {
		if _, err := handler.Do(context.Background(), messagePayload(t, c.Config.AppID, kind, kind, "p2p", `{"sensitive":"never execute this"}`, true)); err != nil {
			t.Fatal(err)
		}
	}
	chats, err := s.Chats(context.Background(), c.ID)
	if err != nil || len(chats) != 3 || len(sender.sent) > 0 {
		t.Fatal("rich message did not remain discovery-only", chats, err)
	}
}

func TestFeishuEventObservationFiltersAndReconnects(t *testing.T) {
	s, _, _, _ := fixture(t)
	c := feishuChannel(t, s, "cli_observe")
	observed := &feishuMessageObservation{now: func() time.Time { return time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC) }}
	s.connections[c.ID] = connectionEntry{generation: "runtime", messages: observed, ConnectionStatus: ConnectionStatus{State: "connected"}}
	handler := feishuEventHandler(c, func() string { return "ou_bot" }, func(in Incoming) error {
		return s.handleIncomingObserved(context.Background(), in, "runtime", observed)
	}, func(result string) { observed.record(result, "") })
	payload := messagePayload(t, c.Config.AppID, "valid", "text", "p2p", `{"text":"sensitive content must not appear in status"}`, true)
	if _, err := handler.Do(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	state, err := s.Connection(context.Background(), c.ID)
	if err != nil || state.MessageCount != 1 || state.LastMessageResult != "discovered" || state.LastMessageAt == nil {
		t.Fatal("missing event/discovery evidence", state, err)
	}
	s.connectionState(c.ID, "runtime", ConnectionStatus{State: "reconnecting"})
	s.connectionState(c.ID, "runtime", ConnectionStatus{State: "connected"})
	state, _ = s.Connection(context.Background(), c.ID)
	if state.MessageCount != 1 || state.LastMessageResult != "discovered" {
		t.Fatal("reconnect erased observation", state)
	}
	var event map[string]any
	json.Unmarshal(messagePayload(t, c.Config.AppID, "ignored", "text", "group", `{"text":"not mentioning bot"}`, true), &event)
	event["event"].(map[string]any)["message"].(map[string]any)["mentions"] = []any{}
	raw, _ := json.Marshal(event)
	if _, err := handler.Do(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	state, _ = s.Connection(context.Background(), c.ID)
	if state.MessageCount != 2 || state.LastMessageResult != "ignored_group" {
		t.Fatal("ignored event was invisible", state)
	}
	chats, _ := s.Chats(context.Background(), c.ID)
	if len(chats) != 1 {
		t.Fatal("unmentioned group was discovered", chats)
	}
	json.Unmarshal(payload, &event)
	event["header"].(map[string]any)["app_id"] = "cli_another"
	raw, _ = json.Marshal(event)
	if _, err := handler.Do(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	state, _ = s.Connection(context.Background(), c.ID)
	if state.LastMessageResult != "invalid_event" {
		t.Fatal("cross-app payload accepted", state)
	}
	raw, _ = json.Marshal(state)
	if strings.Contains(string(raw), "sensitive content") || strings.Contains(string(raw), "private-app-secret") || strings.Contains(string(raw), "ou_operator") {
		t.Fatal("status exposed message or credential/identity material")
	}
	s.stopDiscoveryLocked(c.ID)
	observed.record("received", "")
	state, _ = s.Connection(context.Background(), c.ID)
	if state.State != "stopped" || state.MessageCount != 0 {
		t.Fatal("stopped connection retained obsolete event observation", state)
	}
}

func TestFeishuSDKMessagePayloadDiscoversAllHumanConversationTypes(t *testing.T) {
	s, _, sender, _ := fixture(t)
	c := feishuChannel(t, s, "cli_payload")
	handler := feishuEventHandler(c, func() string { return "ou_bot" }, func(in Incoming) error { return s.HandleIncoming(context.Background(), in) }, func(string) {})
	for _, sample := range []struct {
		id, kind, chat, content string
		tenant                  bool
	}{
		{"plain", "text", "p2p", `{"text":"hello"}`, true},
		{"post", "post", "p2p", `{"zh_cn":{"content":[[{"tag":"text","text":"rich message"}]]}}`, true},
		{"image", "image", "p2p", `{"image_key":"sensitive-image-key"}`, true},
		{"header_tenant", "text", "p2p", `{"text":"hello"}`, false},
		{"group", "text", "group", `{"text":"@_user_1 hello"}`, true},
	} {
		if _, err := handler.Do(context.Background(), messagePayload(t, c.Config.AppID, sample.id, sample.kind, sample.chat, sample.content, sample.tenant)); err != nil {
			t.Fatal(err)
		}
	}
	chats, err := s.Chats(context.Background(), c.ID)
	if err != nil || len(chats) != 5 {
		t.Fatalf("SDK messages were silently lost: discovered %d of 5 conversations (%v)", len(chats), err)
	}
	if len(sender.sent) > 0 {
		t.Fatal("notification discovery invoked chat or sent replies")
	}
}
