package notification

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
)

func TestNotificationCardWordingContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []struct {
		Event     event.Event `json:"event"`
		Timestamp string      `json:"timestamp"`
		Zh, En    MessageCard
	}
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	for _, sample := range samples {
		for language, expected := range map[string]MessageCard{"zh-CN": sample.Zh, "en-US": sample.En} {
			actual := buildMessageCard(sample.Event, language, sample.Timestamp)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("%s/%s: got %#v, want %#v", sample.Event.Type, language, actual, expected)
			}
		}
	}
}

func TestNotificationExitDetailsAndMissingNames(t *testing.T) {
	card := buildMessageCard(event.Event{Type: "container.exited", Title: "redis", NodeName: "redis", ResourceID: strings.Repeat("a", 64)}, "en-US", "")
	if card.Fields[0].Label != "Container" || card.Fields[0].Value != "redis" {
		t.Fatal("a container sharing its node's name lost its identity", card)
	}
	for _, text := range []string{"die exit=137", "State reconciliation: exited with code 137", "exit code 137"} {
		for _, title := range []string{"", "Container exited unexpectedly", strings.Repeat("a", 64)} {
			card := buildMessageCard(event.Event{Type: "container.exited", Severity: "warning", Title: title, ResourceID: strings.Repeat("a", 64), NodeID: "123", Message: text}, "en-US", "now")
			raw := encode(card)
			if !strings.Contains(raw, "Name unavailable") || !strings.Contains(raw, "Node name unavailable") || !strings.Contains(raw, "SIGKILL") || !strings.Contains(raw, `"value":"137"`) || strings.Contains(raw, strings.Repeat("a", 13)) {
				t.Fatal("missing metadata was presented as a name or exit code was lost", raw)
			}
		}
	}
	for code, detail := range map[string]string{"1": "non-zero", "126": "permissions", "127": "not found", "139": "SIGSEGV", "143": "SIGTERM"} {
		card := buildMessageCard(event.Event{Type: "container.exited", Message: "die exit=" + code}, "en-US", "")
		if !strings.Contains(encode(card), detail) {
			t.Fatal(code, card)
		}
	}
}

func TestNativeNotificationPresentationColorsEscapingAndDigestLimits(t *testing.T) {
	_, color := messageStyle(Message{Cards: []MessageCard{{Status: "success"}}, Events: []event.Event{{Severity: "critical", Type: "task.failed"}}})
	if color != "red" {
		t.Fatal("digest hid an urgent event beyond the visible entries")
	}
	for status, color := range map[string]string{"warning": "orange", "error": "red", "critical": "red", "success": "green", "recovered": "green", "pending": "orange", "info": "blue"} {
		message := Message{Default: true, Title: "A notice", Cards: []MessageCard{{Title: "A notice", Status: status, Fields: []MessageField{{"Container", "redis_<at id=all>* & <b>unsafe</b>"}}}}}
		card := feishuCard(message)
		if card["header"].(map[string]any)["template"] != color {
			t.Fatal(status, card)
		}
		parts, rich := telegramParts(message)
		if !rich || !strings.Contains(parts[0], "<b>Container</b>") || strings.Contains(parts[0], "<at id=all>") || strings.Contains(parts[0], "<b>unsafe</b>") {
			t.Fatal(parts)
		}
		content := card["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)["content"].(string)
		if strings.Contains(content, "\nredis_<at") || !strings.Contains(content, `\<at`) || !strings.Contains(content, `\*`) || !strings.Contains(content, `\_`) {
			t.Fatal(content)
		}
	}
	rows := []database.NotificationEvent{}
	for i := 0; i < 12; i++ {
		rows = append(rows, eventRow(event.Event{ID: "event", Type: "task.failed", Severity: "error", Title: strings.Repeat("<&", 100), NodeName: strings.Repeat("节点", 120), Project: strings.Repeat("项目", 120), Message: strings.Repeat("&<", 120)}))
	}
	message := renderMessage(DeliverySnapshot{Language: "en-US", Policy: DeliveryPolicy{Timezone: "UTC"}}, rows)
	card := feishuCard(message)
	if len(encode(card)) > 28000 || !strings.Contains(encode(card), "more events in your inbox") || len(message.Events) != 12 {
		t.Fatal("oversized or incomplete digest", len(encode(card)))
	}
	parts, rich := telegramParts(message)
	if !rich || len(parts) < 2 {
		t.Fatal("expected safe HTML chunks")
	}
	for _, part := range parts {
		if len(utf16.Encode([]rune(part))) > 4096 || strings.Count(part, "<b>") != strings.Count(part, "</b>") {
			t.Fatal("invalid HTML chunk", part)
		}
	}
	custom := strings.Repeat("😀<b>&", 2000)
	parts, rich = telegramParts(Message{Text: custom})
	if rich || strings.Join(parts, "") != custom {
		t.Fatal("custom template changed")
	}
	for _, part := range parts {
		if len(utf16.Encode([]rune(part))) > 3500 {
			t.Fatal("UTF-16 limit exceeded")
		}
	}
}

func TestTelegramSendsNativeHTMLAndOnlyOneDetailsButton(t *testing.T) {
	a := NewAdapter()
	calls, buttons := 0, 0
	a.Client = func(bool) *http.Client {
		return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			var body struct {
				Text        string          `json:"text"`
				ParseMode   string          `json:"parse_mode"`
				ReplyMarkup json.RawMessage `json:"reply_markup"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.ParseMode != "HTML" || strings.Contains(body.Text, "<at id=all>") {
				t.Fatal(body)
			}
			if len(body.ReplyMarkup) > 0 {
				buttons++
			}
			calls++
			return response(200, `{"ok":true,"result":{"message_id":42}}`), nil
		})}
	}
	rows := []database.NotificationEvent{}
	for i := 0; i < 8; i++ {
		rows = append(rows, eventRow(event.Event{Type: "container.exited", Severity: "warning", Title: "redis <at id=all>", NodeName: strings.Repeat("Production ", 20), Message: "die exit=137"}))
	}
	message := renderMessage(DeliverySnapshot{Language: "en-US", PublicURL: "https://suma.example", Policy: DeliveryPolicy{Timezone: "UTC"}}, rows)
	if _, err := a.Send(context.Background(), Channel{NotificationChannel: database.NotificationChannel{Provider: "telegram"}, Config: Config{ChatID: "test"}}, Secrets{Token: "private"}, message); err != nil {
		t.Fatal(err)
	}
	if calls < 2 || buttons != 1 {
		t.Fatal(calls, buttons)
	}
}
