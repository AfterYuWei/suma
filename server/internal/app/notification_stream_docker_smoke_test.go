//go:build dockersmoke

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/notification"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

func TestRealDockerBoundChatStreamsNodeStatus(t *testing.T) {
	endpoint := os.Getenv("SUMA_PROJECT_SMOKE_UNIX")
	if os.Getenv("SUMA_RUN_OPERATIONS_SMOKE") != "1" || endpoint == "" {
		t.Skip("run doc/operations-smoke.sh with its isolated Docker engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, store, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	if err := db.Model(&database.Node{}).Where("id = ?", "local").Update("name", "ganzhou").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.User{Username: "smoke-stream-admin", PasswordHash: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	runtime := aiRuntime{db: db, nodes: nodes, tasks: tasks}
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
	var reads atomic.Int32
	assistant, err := ai.NewService(db, store, tasks, ai.Dependencies{Model: &nodeChatStreamModel{release: release}, Read: func(ctx context.Context, id, tool string, args ai.ToolArgs, lines, bytes int) (ai.Evidence, error) {
		reads.Add(1)
		if id != "local" {
			return ai.Evidence{}, ai.ErrScope
		}
		return runtime.Read(ctx, id, tool, args, lines, bytes)
	}, ActorValid: func(ctx context.Context, a ai.Actor) error {
		if a.BindingID != "" {
			b, err := notify.ValidateBinding(ctx, a.UserID, a.BindingID)
			if err != nil || b.ChatID != a.ChatID || b.ExternalUserID != a.ExternalUserID {
				return ai.ErrScope
			}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	cfg := assistant.Settings()
	cfg.Enabled, cfg.Model, cfg.NodeIDs = true, "controlled-stream", []string{"local"}
	if _, err := assistant.SaveSettings(ctx, ai.SettingsInput{Settings: cfg}, ai.Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.TestModel(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := notify.SaveChannel(ctx, "", notification.ChannelInput{Name: "stream", Provider: "feishu_app", Enabled: true, Config: notification.Config{AppID: "cli_stream_smoke", Interactive: true, Language: "zh-CN", Timezone: "UTC"}, Secrets: &notification.Secrets{Token: "fixture-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	b := database.NotificationBinding{ID: "stream-binding", UserID: 1, ChannelID: c.ID, CodeHash: "fixture-code-hash", ExternalUserID: "tenant:operator", ChatID: "oc_private", Status: "active"}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	notify.SetChatHandler(chatOperations(notify, assistant))
	assistant.SetChatDelivery(chatWorkflowDelivery(notify, assistant))
	if err := notify.HandleIncoming(ctx, notification.Incoming{ID: "question", ChannelID: c.ID, UserID: b.ExternalUserID, ChatID: b.ChatID, Private: true, Text: "发送ganzhou节点的信息给我"}); err != nil {
		t.Fatal(err)
	}
	partial := false
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_, updates, _ := sender.snapshot()
		for _, update := range updates {
			if !update.Final && strings.Contains(update.Text, "ganzhou") {
				partial = true
				break
			}
		}
		if partial {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !partial || reads.Load() != 1 {
		t.Fatal("real node read was not streamed before completion", reads.Load())
	}
	close(release)
	complete := false
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_, updates, _ := sender.snapshot()
		for _, update := range updates {
			if update.Final && strings.Contains(update.Text, "Query completed") {
				complete = true
				break
			}
		}
		if complete {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	created, _, ordinary := sender.snapshot()
	if !complete || created != 1 || len(ordinary) != 0 {
		t.Fatal("real streaming query did not finalize one reply", created, len(ordinary))
	}
	for _, model := range []any{&database.AIOperation{}, &database.Task{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("read-only stream created a mutation", err)
		}
	}
	t.Log("verified bound Feishu processing → Eino streaming → real Unix node read → partial and final updates on one receipt; provider/model substitutes, no mutations")
}
