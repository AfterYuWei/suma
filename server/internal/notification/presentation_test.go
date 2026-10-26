package notification

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
)

func TestMessagePresentationContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Event    event.Event `json:"event"`
		Timezone string      `json:"timezone"`
		Zh       string      `json:"zh"`
		En       string      `json:"en"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, sample := range cases {
		for language, expected := range map[string]string{"zh-CN": sample.Zh, "en-US": sample.En} {
			t.Run(sample.Event.Type+"/"+language, func(t *testing.T) {
				zone, _ := time.LoadLocation(sample.Timezone)
				actual := defaultEventText(sample.Event, language, sample.Event.Time.In(zone).Format("2006-01-02 15:04"))
				if actual != expected {
					t.Fatalf("got:\n%s\nwant:\n%s", actual, expected)
				}
			})
		}
	}
}

func eventRow(e event.Event) database.NotificationEvent {
	return database.NotificationEvent{ID: e.ID, DataJSON: encode(e)}
}

func TestConciseNotificationBatchPreservesFullEvidenceAndCustomTemplates(t *testing.T) {
	now := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	e := event.Event{ID: "first", Type: "container.oom", Severity: "error", Time: now, NodeName: "Production", Title: "redis", ResourceID: strings.Repeat("a", 64), Message: strings.Repeat("technical evidence ", 500)}
	snapshot := DeliverySnapshot{Language: "zh-CN", PublicURL: "https://suma.example/", Policy: DeliveryPolicy{Timezone: "Europe/Berlin"}}
	message := renderMessage(snapshot, []database.NotificationEvent{eventRow(e)})
	if message.Text != "容器内存不足\nProduction · redis · 2026-10-25 02:30" || message.URL != "https://suma.example/notifications#first" || len(message.Events) != 1 || message.Events[0].Message != e.Message {
		t.Fatal("default lost concise presentation or full evidence", message.Text)
	}
	if strings.Contains(message.Text, e.ResourceID) || strings.Contains(message.Text, "technical evidence") {
		t.Fatal("raw evidence leaked into default message")
	}
	rows := []database.NotificationEvent{}
	for i := 0; i < 12; i++ {
		rows = append(rows, eventRow(e))
	}
	message = renderMessage(snapshot, rows)
	if len(message.Events) != 12 || strings.Count(message.Text, "容器内存不足") != 8 || !strings.Contains(message.Text, "另有 4 条") || message.URL != "https://suma.example/notifications" {
		t.Fatal("digest silently dropped evidence", message.Text)
	}
	snapshot.Policy.Template = "{{title}}\n{{message}}\n{{node}}\n{{resource}}\n{{time}}\n{{url}}"
	e.Title = "{{message}}"
	e.Message = "literal {{url}}"
	message = renderMessage(snapshot, []database.NotificationEvent{eventRow(e)})
	if !strings.HasPrefix(message.Text, "{{message}}\nliteral {{url}}\nProduction\n") || !strings.Contains(message.Text, "2026-10-25 02:30:00 +01:00") || !strings.HasSuffix(message.Text, "https://suma.example/notifications#first") || message.Default {
		t.Fatal("custom variables were recursively expanded or changed", message.Text)
	}
	message = renderMessage(DeliverySnapshot{Policy: DeliveryPolicy{Timezone: "UTC"}}, []database.NotificationEvent{eventRow(e)})
	if message.URL != "" || strings.Contains(message.Text, "/notifications#") {
		t.Fatal("default advertised an unusable relative link")
	}
}

func TestNotificationActionUsesChannelLanguageWithoutDuplicateCardTitle(t *testing.T) {
	for language, label := range map[string]string{"zh-CN": "查看详情", "en-US": "View details"} {
		message := Message{Title: "Container recovered", Text: "Container recovered\nProduction · redis", URL: "https://suma.example/notifications", Language: language, Default: true}
		raw, _ := json.Marshal(feishuCard(message))
		if strings.Count(string(raw), "Container recovered") != 1 || !strings.Contains(string(raw), label) || strings.Contains(string(raw), "Open SUMA") {
			t.Fatal("card title/action is noisy", string(raw))
		}
		raw, _ = json.Marshal(telegramButtons(message))
		if !strings.Contains(string(raw), label) || strings.Contains(string(raw), " / ") {
			t.Fatal("button language mismatch", string(raw))
		}
	}
}
