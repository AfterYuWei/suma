package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

type workbenchModel struct{}

func (workbenchModel) Complete(_ context.Context, cfg ai.Settings, _ string, _ []ai.ModelMessage, tools []ai.Tool) (ai.ModelReply, error) {
	if len(tools) == 1 && tools[0].Name == "connection_probe" {
		return ai.ModelReply{Calls: []ai.ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test"}`)}}}, nil
	}
	return ai.ModelReply{Text: "## Diagnosis with " + cfg.Model}, nil
}

func TestGlobalAIWorkbenchHTTPModelScopeAuthAndHistory(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "workbench.db"))
	if err != nil {
		t.Fatal(err)
	}
	authn := auth.NewService(db, time.Hour)
	if _, err = authn.Initialize(context.Background(), "admin", "workbench@example.com", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authn.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	db.Create(&database.Node{ID: "local", Name: "Local", Enabled: true})
	store, err := secret.Open(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := ai.NewService(db, store, task.NewService(db), ai.Dependencies{Model: workbenchModel{}})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	cfg := assistant.Settings()
	cfg.Enabled = true
	cfg.NodeIDs = []string{"local"}
	cfg.Model = "default"
	cfg.Models = []string{"default", "alternate"}
	if _, err = assistant.SaveSettings(context.Background(), ai.SettingsInput{Settings: cfg}, ai.Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Auth: authn, AI: assistant})
	request := func(method, path, body, origin string, authorized bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://suma.test/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		if authorized {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	for _, check := range []struct {
		body, origin string
		authorized   bool
		status       int
	}{
		{`{"question":"Global"}`, "http://suma.test", false, 401},
		{`{"question":"Global"}`, "http://foreign.test", true, 403},
		{`{"question":"Unauthorized node","node_id":"foreign"}`, "http://suma.test", true, 403},
		{`{"question":"Unknown model","model":"unconfigured"}`, "http://suma.test", true, 422},
		{`{"question":"Unknown fields","model_id":"alternate"}`, "http://suma.test", true, 400},
	} {
		res := request("POST", "/ai/runs", check.body, check.origin, check.authorized)
		if res.Code != check.status {
			t.Fatal(check, res.Code, res.Body.String())
		}
	}
	var parent string
	for _, model := range []string{"alternate", "default"} {
		res := request("POST", "/ai/runs", `{"question":"Compare authorized nodes","model":"`+model+`","parent_id":"`+parent+`"}`, "http://suma.test", true)
		if res.Code != 202 {
			t.Fatal(res.Code, res.Body.String())
		}
		var reply struct{ Data ai.Run }
		if err = json.Unmarshal(res.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		if reply.Data.Model != model || reply.Data.NodeID != "" || reply.Data.ParentID != parent {
			t.Fatal(reply.Data)
		}
		parent = reply.Data.ID
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			current, err := assistant.Run(context.Background(), parent)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status != "running" {
				if current.Status != "completed" {
					t.Fatal(current)
				}
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	res := request("GET", "/ai/runs", "", "http://suma.test", true)
	var history struct{ Data []ai.Run }
	if err = json.Unmarshal(res.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if res.Code != 200 || len(history.Data) != 2 || assistant.Settings().Model != "default" {
		t.Fatal(res.Code, res.Body.String())
	}
}
