package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

type targetScopeModel struct{}

func (targetScopeModel) Complete(_ context.Context, _ ai.Settings, _ string, messages []ai.ModelMessage, tools []ai.Tool) (ai.ModelReply, error) {
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
		if strings.Contains(messages[i].Text, "full-container-id") {
			return ai.ModelReply{Text: "## 容器\n\n- nginx"}, nil
		}
		if strings.Contains(messages[i].Text, "target_node_ids") {
			return ai.ModelReply{Calls: []ai.ToolCall{{ID: "confirmed-list", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"","kind":"container","id":""}`)}}}, nil
		}
	}
	return ai.ModelReply{Calls: []ai.ToolCall{{ID: "unconfirmed-list", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"aliyun","kind":"container","id":""}`)}}}, nil
}

func TestAIResourceQueryHTTPClarifiesAndResumesWithoutScopeExpansion(t *testing.T) {
	ctx := context.Background()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	authn := auth.NewService(db, time.Hour)
	if _, err := authn.Initialize(ctx, "admin", "targets@example.com", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authn.Login(ctx, "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []database.Node{{ID: "local", Name: "Alibaba Cloud", Enabled: true}, {ID: "foreign", Name: "Private", Enabled: true}} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	assistant, err := ai.NewService(db, store, task.NewService(db), ai.Dependencies{Model: targetScopeModel{}, Resources: func(_ context.Context, node string, args ai.ToolArgs) ([]ai.ResourceOption, error) {
		reads.Add(1)
		if node != "local" || args.Kind != "container" {
			t.Error("unverified runtime was accessed", node, args.Kind)
		}
		return []ai.ResourceOption{{ID: "full-container-id", Name: "nginx", NodeID: node, Kind: "container"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	cfg := assistant.Settings()
	cfg.Enabled, cfg.Model, cfg.NodeIDs = true, "test", []string{"local"}
	if _, err := assistant.SaveSettings(ctx, ai.SettingsInput{Settings: cfg}, ai.Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.TestModel(ctx); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Auth: authn, AI: assistant})
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, "http://suma.test/api/v1"+path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://suma.test")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	create := request("POST", "/ai/conversations", map[string]any{})
	var conversation struct{ Data ai.Conversation }
	if create.Code != 200 || json.Unmarshal(create.Body.Bytes(), &conversation) != nil {
		t.Fatal(create.Code, create.Body.String())
	}
	posted := request("POST", "/ai/conversations/"+conversation.Data.ID+"/messages", ai.MessageInput{Question: "阿里云节点有哪些容器？", RequestID: "same-query"})
	var started struct{ Data ai.Run }
	if posted.Code != 202 || json.Unmarshal(posted.Body.Bytes(), &started) != nil {
		t.Fatal(posted.Code, posted.Body.String())
	}
	wait := func(status string) ai.Run {
		t.Helper()
		until := time.Now().Add(4 * time.Second)
		for time.Now().Before(until) {
			res := request("GET", "/ai/runs/"+started.Data.ID, nil)
			var row struct{ Data ai.Run }
			if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &row) != nil {
				t.Fatal(res.Code, res.Body.String())
			}
			if row.Data.Status == status {
				return row.Data
			}
			if row.Data.Status == "failed" {
				t.Fatal(row.Data.Error)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("HTTP run did not reach", status)
		return ai.Run{}
	}
	waiting := wait("waiting_input")
	if reads.Load() != 0 || waiting.Interaction == nil || waiting.Interaction.Kind != "node" || len(waiting.Interaction.Options) != 1 || waiting.Interaction.Options[0].ID != "local" {
		t.Fatal("HTTP query did not request an authorized node before reading", waiting)
	}
	answer := ai.InputAnswer{InteractionID: waiting.Interaction.ID, ExpectedRevision: waiting.Revision, RequestID: "select-node", Values: []string{"foreign"}}
	if res := request("POST", "/ai/runs/"+started.Data.ID+"/inputs", answer); res.Code != 422 {
		t.Fatal("unlisted target accepted", res.Code, res.Body.String())
	}
	answer.Values = []string{"local"}
	for i := 0; i < 2; i++ {
		if res := request("POST", "/ai/runs/"+started.Data.ID+"/inputs", answer); res.Code != 200 {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	done := wait("completed")
	if reads.Load() != 1 || done.Error != "" || done.TargetSource != "selection" || len(done.TargetNodeIDs) != 1 || done.TargetNodeIDs[0] != "local" || len(done.Result.OperationIDs) != 0 || !strings.Contains(done.Result.Summary, "nginx") {
		t.Fatal("query did not resume a single read in the selected scope", done, reads.Load())
	}
}
