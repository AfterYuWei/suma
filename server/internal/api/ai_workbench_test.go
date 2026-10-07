package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/suma/suma/server/internal/testutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
	db, err := testutil.Open(t)
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

	create := request("POST", "/ai/conversations", "{}", "http://suma.test", true)
	var conversation struct{ Data ai.Conversation }
	if create.Code != 200 || json.Unmarshal(create.Body.Bytes(), &conversation) != nil {
		t.Fatal(create.Code, create.Body.String())
	}
	path := "/ai/conversations/" + conversation.Data.ID + "/messages"
	for _, check := range []struct {
		body, origin string
		authorized   bool
		status       int
	}{
		{`{"question":"task","request_id":"unauth"}`, "http://suma.test", false, 401},
		{`{"question":"task","request_id":"origin"}`, "http://foreign.test", true, 403},
		{`{"question":"task","request_id":"scope","context":{"node_ids":["foreign"]}}`, "http://suma.test", true, 403},
		{`{"question":"task","request_id":"model","model":"unconfigured"}`, "http://suma.test", true, 422},
		{`{"question":"task","request_id":"unknown","model_id":"alternate"}`, "http://suma.test", true, 400},
	} {
		res := request("POST", path, check.body, check.origin, check.authorized)
		if res.Code != check.status {
			t.Fatal(check, res.Code, res.Body.String())
		}
	}
	for i, model := range []string{"alternate", "default"} {
		res := request("POST", path, `{"question":"Explain Docker","request_id":"turn-`+fmt.Sprint(i)+`","model":"`+model+`","context":{"node_ids":["local"]}}`, "http://suma.test", true)
		if res.Code != 202 {
			t.Fatal(res.Code, res.Body.String())
		}
		var reply struct{ Data ai.Run }
		if json.Unmarshal(res.Body.Bytes(), &reply) != nil {
			t.Fatal("decode run")
		}
		if reply.Data.Model != model || reply.Data.ConversationID != conversation.Data.ID {
			t.Fatal(reply.Data)
		}
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			row, err := assistant.Run(context.Background(), reply.Data.ID)
			if err != nil {
				t.Fatal(err)
			}
			if row.Status == "completed" {
				break
			}
			if row.Status == "failed" {
				t.Fatal(row.Error)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	res := request("GET", "/ai/conversations/"+conversation.Data.ID, "", "http://suma.test", true)
	var detail struct{ Data ai.Conversation }
	if json.Unmarshal(res.Body.Bytes(), &detail) != nil || res.Code != 200 || len(detail.Data.Runs) != 2 || len(detail.Data.Messages) != 4 {
		t.Fatal(res.Code, res.Body.String())
	}
	live := httptest.NewServer(router)
	defer live.Close()
	headers := http.Header{"Origin": []string{live.URL}, "Cookie": []string{sessionCookie + "=" + token}}
	socketURL := strings.Replace(live.URL, "http://", "ws://", 1) + "/ws/ai/conversations/" + conversation.Data.ID
	socket, _, err := websocket.DefaultDialer.Dial(socketURL+"?after=0", headers)
	if err != nil {
		t.Fatal(err)
	}
	_ = socket.SetReadDeadline(time.Now().Add(3 * time.Second))
	var firstEvent ai.WorkflowEvent
	if err = socket.ReadJSON(&firstEvent); err != nil {
		t.Fatal(err)
	}
	_ = socket.Close()
	resumed, _, err := websocket.DefaultDialer.Dial(socketURL+"?after="+fmt.Sprint(firstEvent.Seq), headers)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	_ = resumed.SetReadDeadline(time.Now().Add(3 * time.Second))
	var nextEvent ai.WorkflowEvent
	if err = resumed.ReadJSON(&nextEvent); err != nil || nextEvent.Seq != firstEvent.Seq+1 {
		t.Fatal("event replay duplicated or skipped a sequence", err)
	}
	if request("POST", "/ai/runs", `{}`, "http://suma.test", true).Code != 404 {
		t.Fatal("legacy run creation endpoint retained")
	}
}
