package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
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
func TestNotificationAuthenticatedHTTP(t *testing.T) {
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
	notify := notification.NewService(db, store, notification.Dependencies{Sender: notificationSender{}})
	router := NewRouter(Dependencies{Auth: authn, Audit: audit.NewService(db), Tasks: tasks, Notifications: notify})
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
	for _, path := range []string{"/notifications/channels", "/notifications/inbox", "/audit-logs"} {
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
	// An incompatible schema returns a safe fresh-database instruction.
	if err := db.Exec("ALTER TABLE notification_deliveries DROP COLUMN chat_id").Error; err != nil {
		t.Fatal(err)
	}
	updated := notification.ChannelInput{Name: application.Data.Name, Provider: application.Data.Provider, Enabled: false, Version: application.Data.Version, Config: application.Data.Config}
	body, err := json.Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}
	r = request("PUT", path, string(body), true)
	if r.Code != 503 || !strings.Contains(r.Body.String(), `"code":20802`) || strings.Contains(r.Body.String(), "invalid field") || strings.Contains(r.Body.String(), "PRIVATE-APP-SECRET") {
		t.Fatal("schema error was not safe and actionable", r.Code, r.Body.String())
	}
	if err := db.Exec("ALTER TABLE notification_deliveries ADD COLUMN chat_id varchar(128)").Error; err != nil {
		t.Fatal(err)
	}

	r = request("GET", "/audit-logs", "", true)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "notification.channel.save") {
		t.Fatal("notification audit missing", r.Code, r.Body.String())
	}
	for _, path := range []string{"/ai/settings", "/ai/settings/test", "/ai/settings/models", "/ai/conversations", "/ai/conversations/old/messages", "/ai/runs/old", "/ai/runs/old/inputs", "/ai/runs/old/cancel", "/ai/operations", "/ai/operations/old/decision", "/ai/audit", "/notification-bindings", "/notification-bindings/old/confirm"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			for _, authorized := range []bool{false, true} {
				if res := request(method, path, "{}", authorized); res.Code != http.StatusNotFound {
					t.Fatalf("removed route %s %s returned %d", method, path, res.Code)
				}
			}
		}
	}
	ws := httptest.NewRequest("GET", "/ws/ai/conversations/old", nil)
	ws.Header.Set("Origin", "http://example.com")
	ws.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	result := httptest.NewRecorder()
	router.ServeHTTP(result, ws)
	if result.Code != http.StatusNotFound {
		t.Fatalf("removed websocket returned %d", result.Code)
	}
	catalog := request("GET", "/notifications/catalog", "", true)
	if catalog.Code != 200 || strings.Contains(catalog.Body.String(), `"ai.`) || !strings.Contains(catalog.Body.String(), "cd.awaiting_approval") {
		t.Fatal("invalid notification catalog", catalog.Code, catalog.Body.String())
	}
	// Removed configuration fields are rejected instead of silently enabling behavior.
	removedConfig := strings.Replace(create, `"timezone":"UTC"`, `"interactive":true,"timezone":"UTC"`, 1)
	if res := request("POST", "/notifications/channels", removedConfig, true); res.Code != http.StatusBadRequest {
		t.Fatal("removed channel config accepted", res.Code)
	}
	telegram := `{"name":"Telegram","provider":"telegram","enabled":false,"config":{"timezone":"UTC","language":"en-US"},"secrets":{"token":"123:PRIVATE-TOKEN"}}`
	for _, enabled := range []bool{false, true} {
		body := telegram
		if enabled {
			body = strings.Replace(body, `"timezone":"UTC"`, `"auto_discover":true,"timezone":"UTC"`, 1)
		}
		res := request("POST", "/notifications/channels", body, true)
		if res.Code != 200 || !strings.Contains(res.Body.String(), `"auto_discover":`+fmt.Sprint(enabled)) || strings.Contains(res.Body.String(), "PRIVATE-TOKEN") {
			t.Fatal("invalid Telegram discovery config", res.Code, res.Body.String())
		}
	}
}
