//go:build dockersmoke

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestRealDockerGuestChatQueryNamedNode(t *testing.T) {
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
	if err := db.Create(&database.Node{ID: "other", Name: "beijing", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.User{Username: "smoke-admin", PasswordHash: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	runtime := aiRuntime{db: db, nodes: nodes}
	var queries []string
	var summary ai.QuerySummary
	assistant, err := ai.NewService(db, store, task.NewService(db), ai.Dependencies{
		Model: forbiddenGuestModel{t},
		Query: func(ctx context.Context, id string) (ai.QuerySummary, error) {
			queries = append(queries, id)
			var err error
			summary, err = runtime.Query(ctx, id)
			return summary, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	cfg := assistant.Settings()
	cfg.Enabled, cfg.Model, cfg.NodeIDs = true, "unused-model", []string{"local", "other"}
	if _, err := assistant.SaveSettings(ctx, ai.SettingsInput{Settings: cfg}, ai.Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	sender := &querySender{}
	notify := notification.NewService(db, store, notification.Dependencies{Sender: sender})
	channel, err := notify.SaveChannel(ctx, "", notification.ChannelInput{Name: "Feishu smoke", Provider: "feishu_app", Enabled: true, Config: notification.Config{AppID: "cli_smoke_query", Interactive: true, Language: "zh-CN", Timezone: "Asia/Shanghai"}, Secrets: &notification.Secrets{Token: "fixture-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	notify.SetChatHandler(chatOperations(notify, assistant))
	if err := notify.HandleIncoming(ctx, notification.Incoming{ID: "query", ChannelID: channel.ID, UserID: "tenant:guest", ChatID: "oc_private", Private: true, Text: "发送ganzhou节点的信息给我"}); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0] != "local" || len(sender.messages) != 1 {
		t.Fatal("natural-language request did not query its named real runtime", queries)
	}
	if !summary.Available || summary.MissingContainers || summary.MissingImages {
		t.Fatal("real Docker container/image reads were unavailable")
	}
	text := sender.messages[0].Text
	if !strings.Contains(text, "Node: 可用 / available") || !strings.Contains(text, "Containers:") || !strings.Contains(text, "Images:") {
		t.Fatal("real Docker status summary was unavailable", text)
	}
	for _, model := range []any{&database.AIRun{}, &database.AIOperation{}, &database.Task{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("read-only chat created model work or a mutation", err)
		}
	}
}
