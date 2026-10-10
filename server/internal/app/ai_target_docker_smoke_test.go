//go:build dockersmoke

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

type clarifiedContainerSmokeModel struct{}

func (clarifiedContainerSmokeModel) Complete(_ context.Context, _ ai.Settings, _ string, messages []ai.ModelMessage, tools []ai.Tool) (ai.ModelReply, error) {
	if len(tools) == 1 && tools[0].Name == "connection_probe" {
		return ai.ModelReply{Calls: []ai.ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test"}`)}}}, nil
	}
	if len(tools) == 0 {
		return ai.ModelReply{Text: `{"general":true}`}, nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "tool" {
			continue
		}
		if messages[i].CallID == "verified-list" {
			return ai.ModelReply{Text: "Confirmed node containers: " + messages[i].Text}, nil
		}
		if strings.Contains(messages[i].Text, "target_node_ids") {
			return ai.ModelReply{Calls: []ai.ToolCall{{ID: "verified-list", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"local","kind":"container","id":""}`)}}}, nil
		}
	}
	return ai.ModelReply{Calls: []ai.ToolCall{{ID: "unverified-list", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"aliyun","kind":"container","id":""}`)}}}, nil
}

func TestRealDockerAIContainerQueryClarifiesTarget(t *testing.T) {
	endpoint := os.Getenv("SUMA_PROJECT_SMOKE_UNIX")
	if os.Getenv("SUMA_RUN_OPERATIONS_SMOKE") != "1" || endpoint == "" {
		t.Skip("run doc/operations-smoke.sh with its isolated Docker engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	if err := db.Model(&database.Node{}).Where("id = ?", "local").Update("name", "Alibaba Cloud").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.User{Username: "target-smoke-admin", PasswordHash: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	runtime := aiRuntime{db: db, nodes: nodes, tasks: tasks}
	var reads atomic.Int32
	assistant, err := ai.NewService(db, store, tasks, ai.Dependencies{Model: clarifiedContainerSmokeModel{}, Resources: func(ctx context.Context, node string, args ai.ToolArgs) ([]ai.ResourceOption, error) {
		reads.Add(1)
		if node != "local" || args.Kind != "container" {
			return nil, ai.ErrScope
		}
		return runtime.Resources(ctx, node, args)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	cfg := assistant.Settings()
	cfg.Enabled, cfg.Model, cfg.NodeIDs = true, "controlled-target", []string{"local"}
	actor := ai.Actor{UserID: 1, Source: "site"}
	if _, err := assistant.SaveSettings(ctx, ai.SettingsInput{Settings: cfg}, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.TestModel(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := assistant.Start(ctx, ai.RunInput{Question: "阿里云节点有哪些容器？"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(status string) ai.Run {
		t.Helper()
		for {
			row, err := assistant.Run(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if row.Status == status {
				return row
			}
			if row.Status == "failed" {
				t.Fatal(row.Error)
			}
			select {
			case <-ctx.Done():
				t.Fatal("waiting for", status, ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	waiting := wait("waiting_input")
	if reads.Load() != 0 || waiting.Interaction == nil || len(waiting.TargetNodeIDs) != 0 {
		t.Fatal("Docker accessed before confirming a target", waiting, reads.Load())
	}
	if _, err := assistant.AnswerInput(ctx, run.ID, ai.InputAnswer{InteractionID: waiting.Interaction.ID, ExpectedRevision: waiting.Revision, RequestID: "select-local", Values: []string{"local"}}, actor); err != nil {
		t.Fatal(err)
	}
	done := wait("completed")
	if reads.Load() != 1 || done.Reads != 1 || done.NodeID != "local" || done.TargetSource != "selection" || done.Error != "" || len(done.Result.OperationIDs) != 0 {
		t.Fatal("confirmed query did not remain a single real Docker read", done, reads.Load())
	}
	actual, err := runtime.Resources(ctx, "local", ai.ToolArgs{Kind: "container"})
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) == 0 || !strings.Contains(done.Result.Summary, "suma-target-query-smoke") {
		t.Fatal("isolated container fixture was not returned by the real query")
	}
	for _, container := range actual {
		if !strings.Contains(done.Result.Summary, container.ID) {
			t.Fatal("real container was omitted from the query result", container.Name)
		}
	}
	var count int64
	if err := db.Model(&database.Task{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("container query created a mutation Task", count, err)
	}
}
