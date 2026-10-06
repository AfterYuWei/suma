//go:build dockersmoke

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

type globalReadSmokeModel struct {
	container string
	read      bool
}

func (m *globalReadSmokeModel) Complete(_ context.Context, _ ai.Settings, _ string, _ []ai.ModelMessage, tools []ai.Tool) (ai.ModelReply, error) {
	if len(tools) == 1 && tools[0].Name == "connection_probe" {
		return ai.ModelReply{Calls: []ai.ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test"}`)}}}, nil
	}
	if len(tools) > 0 && !m.read {
		m.read = true
		calls := []ai.ToolCall{{ID: "node", Name: "read_status", Arguments: json.RawMessage(`{"node_id":"local","kind":"node","id":"local"}`)}}
		if m.container != "" {
			raw, _ := json.Marshal(ai.ToolArgs{NodeID: "local", Kind: "container", ID: m.container})
			calls = append(calls, ai.ToolCall{ID: "container", Name: "read_status", Arguments: raw})
		}
		return ai.ModelReply{Calls: calls}, nil
	}
	return ai.ModelReply{Text: "## Read-only global diagnosis completed"}, nil
}

func TestRealDockerGlobalAIWorkbenchReadOnly(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1 to read the local Docker engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "global-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&database.User{Username: "smoke-user", PasswordHash: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, store, "unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	adapter, err := nodes.Runtime(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	containers, err := adapter.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	model := &globalReadSmokeModel{}
	if len(containers) > 0 {
		model.container = containers[0].ID
	}
	tasks := task.NewService(db)
	runtime := aiRuntime{db: db, nodes: nodes, tasks: tasks}
	assistant, err := ai.NewService(db, store, tasks, ai.Dependencies{Model: model, Read: runtime.Read})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	cfg := assistant.Settings()
	cfg.Enabled = true
	cfg.Model = "smoke-model"
	cfg.NodeIDs = []string{"local"}
	if _, err = assistant.SaveSettings(ctx, ai.SettingsInput{Settings: cfg}, ai.Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = assistant.TestModel(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := assistant.Start(ctx, ai.RunInput{Question: "Inspect authorized Docker status without changing anything"}, ai.Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	for {
		run, err = assistant.Run(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != "running" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	expected := 1
	if model.container != "" {
		expected++
	}
	if run.Status != "completed" || run.NodeID != "" || len(run.Result.Evidence) != expected || len(run.Result.OperationIDs) != 0 {
		t.Fatal("global diagnosis did not remain read-only", run.Status, run.Error)
	}
	for _, evidence := range run.Result.Evidence {
		if evidence.NodeID != "local" || evidence.Unavailable || evidence.Content == "" {
			t.Fatal("real evidence unavailable", evidence.Source)
		}
	}
	var count int64
	if err = db.Model(&database.Task{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("read-only diagnosis created a Task", count, err)
	}
}
