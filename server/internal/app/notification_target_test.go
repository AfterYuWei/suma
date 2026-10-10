package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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

type semanticChatModel struct{ classifications atomic.Int32 }

func (m *semanticChatModel) Complete(_ context.Context, _ ai.Settings, _ string, messages []ai.ModelMessage, tools []ai.Tool) (ai.ModelReply, error) {
	if len(tools) == 1 && tools[0].Name == "connection_probe" {
		return ai.ModelReply{Calls: []ai.ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test"}`)}}}, nil
	}
	if len(tools) == 0 {
		m.classifications.Add(1)
		return ai.ModelReply{Text: `{"general":false,"candidate_node_ids":["local"]}`}, nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			return ai.ModelReply{Text: "Confirmed containers: " + messages[i].Text}, nil
		}
	}
	return ai.ModelReply{Calls: []ai.ToolCall{{ID: "confirmed-list", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"","kind":"container","id":""}`)}}}, nil
}

func TestBoundChatSemanticNodeCanBeConfirmedOrRejectedWithoutOperationApproval(t *testing.T) {
	ctx := context.Background()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.User{Username: "alias-admin", PasswordHash: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []database.Node{{ID: "local", Name: "aliyun", Enabled: true}, {ID: "remote", Name: "Hetzner", Enabled: true}} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	sender := &chatStreamSender{}
	notify := notification.NewService(db, store, notification.Dependencies{Sender: sender})
	defer notify.Stop()
	model := &semanticChatModel{}
	var reads atomic.Int32
	var readNode atomic.Value
	assistant, err := ai.NewService(db, store, task.NewService(db), ai.Dependencies{Model: model, ActorValid: func(ctx context.Context, a ai.Actor) error {
		if a.BindingID != "" {
			b, err := notify.ValidateBinding(ctx, a.UserID, a.BindingID)
			if err != nil || b.ChatID != a.ChatID || b.ExternalUserID != a.ExternalUserID {
				return ai.ErrScope
			}
		}
		return nil
	}, Resources: func(_ context.Context, node string, args ai.ToolArgs) ([]ai.ResourceOption, error) {
		reads.Add(1)
		readNode.Store(node)
		return []ai.ResourceOption{{ID: "container-" + node, Name: "nginx", NodeID: node, Kind: args.Kind}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	cfg := assistant.Settings()
	cfg.Enabled, cfg.Model, cfg.NodeIDs = true, "fixture", []string{"local", "remote"}
	if _, err := assistant.SaveSettings(ctx, ai.SettingsInput{Settings: cfg}, ai.Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.TestModel(ctx); err != nil {
		t.Fatal(err)
	}
	channel, err := notify.SaveChannel(ctx, "", notification.ChannelInput{Name: "alias", Provider: "feishu_app", Enabled: true, Config: notification.Config{AppID: "cli_alias", Interactive: true, Language: "zh-CN", Timezone: "UTC"}, Secrets: &notification.Secrets{Token: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	b := database.NotificationBinding{ID: "alias-binding", UserID: 1, ChannelID: channel.ID, CodeHash: "fixture-code-hash", ExternalUserID: "tenant:operator", ChatID: "oc_private", Status: "active"}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	actor := ai.Actor{UserID: 1, Source: "chat", BindingID: b.ID, ExternalUserID: b.ExternalUserID, ChatID: b.ChatID}
	notify.SetChatHandler(chatOperations(notify, assistant))
	assistant.SetChatDelivery(chatWorkflowDelivery(notify, assistant))
	send := func(id, text string) {
		t.Helper()
		if err := notify.HandleIncoming(ctx, notification.Incoming{ID: id, ChannelID: channel.ID, UserID: b.ExternalUserID, ChatID: b.ChatID, Private: true, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(status string) ai.Run {
		t.Helper()
		until := time.Now().Add(4 * time.Second)
		for time.Now().Before(until) {
			conversation, err := assistant.CreateConversation(ctx, ai.ConversationInput{}, actor)
			if err != nil {
				t.Fatal(err)
			}
			if conversation.CurrentRun != nil {
				if conversation.CurrentRun.Status == status {
					return *conversation.CurrentRun
				}
				if conversation.CurrentRun.Status == "failed" {
					t.Fatal(conversation.CurrentRun.Error)
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("chat did not reach", status)
		return ai.Run{}
	}
	send("question", "阿里云节点有哪些容器？")
	waiting := wait("waiting_input")
	if reads.Load() != 0 || waiting.Interaction == nil || !waiting.Interaction.Options[0].Suggested || !strings.Contains(waiting.Interaction.Prompt, "你指的是 aliyun 节点吗") {
		t.Fatal("semantic node was queried without confirmation", waiting)
	}
	until := time.Now().Add(3 * time.Second)
	for {
		_, _, messages := sender.snapshot()
		found := false
		for _, message := range messages {
			found = found || strings.Contains(message.Text, "Reply yes to confirm this node")
		}
		if found {
			break
		}
		if time.Now().After(until) {
			t.Fatal("chat did not deliver the natural confirmation guide")
		}
		time.Sleep(10 * time.Millisecond)
	}
	classified := model.classifications.Load()
	send("not-this-node", "不是")
	rejected := wait("waiting_input")
	if rejected.ID != waiting.ID || reads.Load() != 0 || model.classifications.Load() != classified {
		t.Fatal("rejecting a suggestion started a query or another model task")
	}
	send("confirm-this-node", "是的！")
	done := wait("completed")
	if done.ID != waiting.ID || done.NodeID != "local" || reads.Load() != 1 || readNode.Load() != "local" || len(done.Result.OperationIDs) != 0 {
		t.Fatal("natural confirmation did not resume the same query", done)
	}
	send("confirm-this-node", "是的！")
	send("late-confirmation", "yes")
	still := wait("completed")
	if still.ID != done.ID || reads.Load() != 1 {
		t.Fatal("duplicate or late confirmation created another run")
	}
	send("second-question", "阿里云节点有哪些容器？")
	second := wait("waiting_input")
	send("pick-another", "Hetzner")
	other := wait("completed")
	if other.ID != second.ID || other.NodeID != "remote" || reads.Load() != 2 || readNode.Load() != "remote" {
		t.Fatal("exact-name reply did not reject the suggestion and preserve the question", other)
	}
	approval := &ai.Run{AIRun: database.AIRun{Status: "waiting_approval"}}
	if !chatNodeConfirmation(ctx, assistant, approval, actor, "确认", "not-an-approval", func(message string) {
		if !strings.Contains(message, "full preview") {
			t.Error("natural yes failed to explain individual operation approval")
		}
	}) {
		t.Fatal("natural confirmation escaped the node-selection boundary")
	}
	var count int64
	if err := db.Model(&database.Task{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("node confirmation created an operation Task", count, err)
	}
}

func TestChatSemanticConfirmationRequiresUniqueCurrentNode(t *testing.T) {
	for _, check := range []struct {
		name, status, kind string
		multiple, handled  bool
		options            []ai.ResourceOption
	}{
		{name: "multiple_candidates", status: "waiting_input", kind: "node", handled: true, options: []ai.ResourceOption{{ID: "a", Suggested: true}, {ID: "b", Suggested: true}}},
		{name: "no_suggestion", status: "waiting_input", kind: "node", handled: true, options: []ai.ResourceOption{{ID: "a"}}},
		{name: "multiple_selection", status: "waiting_input", kind: "node", multiple: true, handled: true},
		{name: "operation_approval", status: "waiting_approval", kind: "approval", handled: true},
		{name: "late_confirmation", status: "completed", kind: "node", handled: true},
		{name: "parameter_uses_existing_flow", status: "waiting_input", kind: "parameter", handled: false},
		{name: "resource_uses_existing_flow", status: "waiting_input", kind: "resource", handled: false},
	} {
		t.Run(check.name, func(t *testing.T) {
			run := &ai.Run{AIRun: database.AIRun{Status: check.status}, Interaction: &ai.Interaction{AIInteraction: database.AIInteraction{Kind: check.kind, Multiple: check.multiple}, Options: check.options}}
			replied := false
			// No service exists: an ambiguous, late or approval reply must never
			// reach an answer/decision API. Unrelated inputs retain their old flow.
			got := chatNodeConfirmation(context.Background(), nil, run, ai.Actor{}, "是", "message", func(string) { replied = true })
			if got != check.handled || replied != check.handled {
				t.Fatal("reply escaped the current unique node confirmation", got, replied)
			}
		})
	}
}
