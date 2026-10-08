package api

import (
	"context"
	"encoding/json"
	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/notification"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type notificationSender struct{}

func (notificationSender) Send(context.Context, notification.Channel, notification.Secrets, notification.Message) (string, error) {
	return "sent", nil
}
func TestNotificationAndAIAuthenticatedHTTP(t *testing.T) {
	dir := t.TempDir()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	authn := auth.NewService(db, time.Hour)
	if _, err = authn.Initialize(context.Background(), "admin", "admin@test.example", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authn.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	assistant, err := ai.NewService(db, store, tasks, ai.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	notify := notification.NewService(db, store, notification.Dependencies{Sender: notificationSender{}})
	router := NewRouter(Dependencies{Auth: authn, Audit: audit.NewService(db), Tasks: tasks, AI: assistant, Notifications: notify})
	request := func(method, path, body string, authorized bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://example.com")
		if authorized {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	for _, path := range []string{"/notifications/channels", "/notifications/inbox", "/notification-bindings", "/ai/settings", "/ai/operations", "/ai/conversations", "/ai/audit", "/audit-logs"} {
		if r := request("GET", path, "", false); r.Code != 401 {
			t.Fatalf("unprotected %s: %d", path, r.Code)
		}
	}
	create := `{"name":"Webhook","provider":"webhook","enabled":false,"version":0,"config":{"endpoint":"https://example.com/SECRETURL","timezone":"UTC","language":"en-US"},"secrets":{"authorization":"Bearer SECRETAUTH"}}`
	r := request("POST", "/notifications/channels", create, true)
	if r.Code != 200 || strings.Contains(r.Body.String(), "SECRET") {
		t.Fatal(r.Code, r.Body.String())
	}
	var created struct{ Data notification.Channel }
	json.Unmarshal(r.Body.Bytes(), &created)
	r = request("POST", "/notifications/channels/"+created.Data.ID+"/test", "{}", true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var conv struct{ Data ai.Conversation }
	createdConv := request("POST", "/ai/conversations", `{}`, true)
	_ = json.Unmarshal(createdConv.Body.Bytes(), &conv)
	r = request("POST", "/ai/conversations/"+conv.Data.ID+"/messages", `{"request_id":"disabled","question":"restart"}`, true)
	if r.Code != 403 {
		t.Fatal("disabled AI accepted diagnosis", r.Code)
	}
	r = request("POST", "/ai/operations/op/decision", `{"approve":true,"review_token":"x","parameters":{"force":true}}`, true)
	if r.Code != 400 {
		t.Fatal("mutated approval accepted", r.Code)
	}
	r = request("GET", "/notifications/channels", "", true)
	if strings.Contains(r.Body.String(), "SECRET") {
		t.Fatal("secret returned in list")
	}
	feishu := `{"name":"Feishu","provider":"feishu_app","enabled":false,"config":{"app_id":"cli_http","targets":[{"chat_id":"oc_private","name":"Admin"},{"chat_id":"oc_group","name":"Operations"}],"timezone":"UTC","language":"en-US"},"secrets":{"token":"PRIVATE-APP-SECRET"}}`
	r = request("POST", "/notifications/channels", feishu, true)
	if r.Code != 200 || strings.Contains(r.Body.String(), "PRIVATE-APP-SECRET") {
		t.Fatal("Feishu setup failed or leaked secret", r.Code)
	}
	var application struct{ Data notification.Channel }
	if err := json.Unmarshal(r.Body.Bytes(), &application); err != nil {
		t.Fatal(err)
	}
	path := "/notifications/channels/" + application.Data.ID
	if r := request("GET", path+"/connection", "", false); r.Code != 401 {
		t.Fatal("unprotected connection status", r.Code)
	}
	if r := request("GET", path+"/connection", "", true); r.Code != 200 || !strings.Contains(r.Body.String(), `"state":"stopped"`) {
		t.Fatal("paused connection status", r.Code, r.Body.String())
	}
	for _, body := range []string{`{}`, `{"chat_id":"oc_unrelated"}`} {
		if r := request("POST", path+"/test", body, true); r.Code != 422 {
			t.Fatal("test accepted unspecified/unrelated recipient", r.Code)
		}
	}
	if r := request("POST", path+"/test", `{"chat_id":"oc_group"}`, true); r.Code != 200 || !strings.Contains(r.Body.String(), `"chat_id":"oc_group"`) {
		t.Fatal("explicit test failed", r.Code)
	}
	if r := request("POST", path+"/check", `{}`, true); r.Code != 200 || !strings.Contains(r.Body.String(), `"credentials_valid":true`) {
		t.Fatal("credential result confused connection status", r.Code)
	}
	if r := request("POST", "/notifications/channels", feishu, true); r.Code != 422 {
		t.Fatal("duplicate application accepted", r.Code)
	}
	if r := request("POST", "/notifications/channels", strings.Replace(feishu, `"feishu_app"`, `"feishu_webhook"`, 1), true); r.Code != 422 {
		t.Fatal("removed provider accepted", r.Code)
	}
	ruleBody := `{"name":"Routed","enabled":true,"config":{"events":["container.oom"],"channel_ids":["` + application.Data.ID + `"],"channel_targets":{"` + application.Data.ID + `":["oc_group"]},"timezone":"UTC","mode":"immediate","recovery":true}}`
	if r := request("POST", "/notifications/rules", ruleBody, true); r.Code != 200 {
		t.Fatal("explicit rule routing failed", r.Code, r.Body.String())
	}
	if r := request("POST", "/notifications/rules", strings.Replace(ruleBody, "oc_group", "oc_unrelated", 1), true); r.Code != 422 {
		t.Fatal("invalid rule recipient accepted", r.Code)
	}
	globalAudit := audit.NewService(db)
	db.Create(&database.AIRun{ID: "audit-run", NodeID: "local", UserID: 1, Status: "completed"})
	db.Create(&database.AIOperation{ID: "audit-operation", RunID: "audit-run", NodeID: "local", TaskID: "audit-task", Status: "completed"})
	if err := globalAudit.RecordAI(context.Background(), db, database.AIAudit{RunID: "audit-run", OperationID: "audit-operation", Action: "execution", Source: "chat", UserID: 1, ExternalUserID: "platform-user", Resource: "container", Result: "completed: AppSecret=private-http-value"}); err != nil {
		t.Fatal(err)
	}
	r = request("GET", "/audit-logs", "", true)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "ai.execution") || !strings.Contains(r.Body.String(), "notification.channel.save") || !strings.Contains(r.Body.String(), "audit-task") || strings.Contains(r.Body.String(), "private-http-value") {
		t.Fatal("global audit did not include both domains safely", r.Code, r.Body.String())
	}
	var global struct{ Data []database.AuditLog }
	json.Unmarshal(r.Body.Bytes(), &global)
	var globalID uint
	for _, row := range global.Data {
		if row.Action == "ai.execution" {
			globalID = row.ID
		}
	}
	r = request("GET", "/ai/audit", "", true)
	var part struct{ Data []database.AIAudit }
	json.Unmarshal(r.Body.Bytes(), &part)
	if r.Code != 200 || len(part.Data) != 1 || part.Data[0].ID != globalID || part.Data[0].TaskID != "audit-task" || strings.Contains(r.Body.String(), "notification.channel.save") || strings.Contains(r.Body.String(), "private-http-value") {
		t.Fatal("AI audit is not the filtered global record", r.Code, r.Body.String())
	}
	r = request("GET", "/audit-logs?scope=control_plane", "", true)
	if strings.Contains(r.Body.String(), "ai.execution") {
		t.Fatal("node audit appeared in control-plane scope")
	}
}
