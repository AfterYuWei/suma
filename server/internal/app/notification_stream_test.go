package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/notification"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

type chatStreamSender struct {
	mu       sync.Mutex
	updates  []notification.StreamUpdate
	ordinary []notification.Message
	created  int
}

func (s *chatStreamSender) Send(_ context.Context, _ notification.Channel, _ notification.Secrets, message notification.Message) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ordinary = append(s.ordinary, message)
	return "ordinary", nil
}
func (s *chatStreamSender) UpdateStream(_ context.Context, _ notification.Channel, _ notification.Secrets, _ string, receipt notification.StreamReceipt, update notification.StreamUpdate) (notification.StreamReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if receipt.MessageID == "" {
		s.created++
		receipt.CardID, receipt.MessageID, receipt.Mode = "card_one", "om_one", "cardkit"
	}
	s.updates = append(s.updates, update)
	receipt.Sequence++
	receipt.Text, receipt.Status, receipt.Closed = update.Text, update.Status, update.Final
	return receipt, nil
}
func (s *chatStreamSender) snapshot() (int, []notification.StreamUpdate, []notification.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.created, append([]notification.StreamUpdate{}, s.updates...), append([]notification.Message{}, s.ordinary...)
}

type nodeChatStreamModel struct {
	call    atomic.Int32
	release <-chan struct{}
}

func (*nodeChatStreamModel) Complete(_ context.Context, _ ai.Settings, _ string, _ []ai.ModelMessage, tools []ai.Tool) (ai.ModelReply, error) {
	if len(tools) == 1 && tools[0].Name == "connection_probe" {
		return ai.ModelReply{Calls: []ai.ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test"}`)}}}, nil
	}
	return ai.ModelReply{Text: "Connection confirmed"}, nil
}
func (m *nodeChatStreamModel) Stream(ctx context.Context, _ ai.Settings, _ string, messages []ai.ModelMessage, _ []ai.Tool, emit func(string) error) (ai.ModelReply, error) {
	if m.call.Add(1) == 1 {
		return ai.ModelReply{Calls: []ai.ToolCall{{ID: "node-read", Name: "read_status", Arguments: json.RawMessage(`{"node_id":"local","kind":"node","id":"local"}`)}}}, nil
	}
	var evidence string
	for _, message := range messages {
		if message.Role == "tool" {
			evidence = message.Text
		}
	}
	text := "## ganzhou 节点信息\n\n```json\n" + evidence + "\n```\n"
	if err := emit(text); err != nil {
		return ai.ModelReply{}, err
	}
	select {
	case <-ctx.Done():
		return ai.ModelReply{}, ctx.Err()
	case <-m.release:
	}
	end := "\n查询完成 / Query completed."
	if err := emit(end); err != nil {
		return ai.ModelReply{}, err
	}
	return ai.ModelReply{Text: text + end, Tokens: 18}, nil
}

func TestBoundFeishuReplyUpdatesBeforeModelCompletionAndUsesOneReceipt(t *testing.T) {
	ctx := context.Background()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.User{Username: "stream-admin", PasswordHash: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Node{ID: "local", Name: "ganzhou", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	sender := &chatStreamSender{}
	notify := notification.NewService(db, store, notification.Dependencies{Sender: sender})
	defer notify.Stop()
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	model := &nodeChatStreamModel{release: release}
	var reads atomic.Int32
	assistant, err := ai.NewService(db, store, task.NewService(db), ai.Dependencies{Model: model, ActorValid: func(ctx context.Context, a ai.Actor) error {
		if a.BindingID != "" {
			b, err := notify.ValidateBinding(ctx, a.UserID, a.BindingID)
			if err != nil || b.ChatID != a.ChatID || b.ExternalUserID != a.ExternalUserID {
				return ai.ErrScope
			}
		}
		return nil
	}, Read: func(_ context.Context, node, tool string, _ ai.ToolArgs, _, _ int) (ai.Evidence, error) {
		reads.Add(1)
		if node != "local" {
			return ai.Evidence{}, ai.ErrScope
		}
		return ai.Evidence{Source: tool, Content: `{"status":"online","engine_version":"fixture-docker"}`, Time: time.Now()}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	cfg := assistant.Settings()
	cfg.Enabled, cfg.Model, cfg.NodeIDs = true, "fixture", []string{"local"}
	if _, err := assistant.SaveSettings(ctx, ai.SettingsInput{Settings: cfg}, ai.Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.TestModel(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := notify.SaveChannel(ctx, "", notification.ChannelInput{Name: "stream", Provider: "feishu_app", Enabled: true, Config: notification.Config{AppID: "cli_stream", Interactive: true, Language: "zh-CN", Timezone: "UTC"}, Secrets: &notification.Secrets{Token: "app-secret-fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	b := database.NotificationBinding{ID: "binding", UserID: 1, ChannelID: c.ID, CodeHash: "fixture-code-hash", ExternalUserID: "tenant:operator", ChatID: "oc_private", Status: "active"}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	notify.SetChatHandler(chatOperations(notify, assistant))
	assistant.SetChatDelivery(chatWorkflowDelivery(notify, assistant))
	in := notification.Incoming{ID: "question", ChannelID: c.ID, UserID: b.ExternalUserID, ChatID: b.ChatID, Private: true, Text: "发送ganzhou节点的信息给我"}
	if err := notify.HandleIncoming(ctx, in); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	visible := false
	for time.Now().Before(deadline) {
		_, updates, _ := sender.snapshot()
		for _, update := range updates {
			if !update.Final && strings.Contains(update.Text, "ganzhou 节点信息") {
				visible = true
				break
			}
		}
		if visible {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !visible {
		_, updates, ordinary := sender.snapshot()
		var state database.AIRun
		_ = db.First(&state).Error
		t.Fatal("Feishu never displayed real streamed output before model completion", updates, ordinary, reads.Load(), state.Status, state.Error)
	}
	var runs []database.AIRun
	if err := db.Find(&runs).Error; err != nil || len(runs) != 1 || runs[0].Status != "running" {
		t.Fatal("model completed before partial delivery", err)
	}
	close(release)
	final := false
	deadline = time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		_, updates, _ := sender.snapshot()
		for _, update := range updates {
			if update.Final && strings.Contains(update.Text, "Query completed") && strings.Contains(update.Text, "fixture-docker") {
				final = true
				break
			}
		}
		if final {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	created, updates, ordinary := sender.snapshot()
	if !final || created != 1 || len(ordinary) != 0 || reads.Load() != 1 {
		t.Fatal("reply was duplicated, not finalized, or used an incorrect runtime", created, len(updates), len(ordinary), reads.Load())
	}
	for _, update := range updates {
		if strings.Contains(update.Text, "app-secret-fixture") {
			t.Fatal("stream exposed provider credentials")
		}
	}
	var records []database.NotificationChatStream
	if err := db.Find(&records).Error; err != nil || len(records) != 1 || !records[0].Closed {
		t.Fatal("closed stream receipt was not retained", err)
	}
}
