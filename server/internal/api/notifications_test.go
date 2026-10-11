package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/event"
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
	create := `{"name":"Webhook","provider":"webhook","enabled":false,"version":0,"config":{"endpoint":"https://example.com/SECRETURL","subscription":{"rule_ids":[],"policy":{"timezone":"UTC","mode":"immediate"}},"language":"en-US"},"secrets":{"authorization":"Bearer SECRETAUTH"}}`
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
	feishu := `{"name":"Feishu","provider":"feishu_app","enabled":false,"config":{"app_id":"cli_http","targets":[{"chat_id":"oc_private","name":"Admin"},{"chat_id":"oc_group","name":"Operations"}],"subscription":{"rule_ids":[],"policy":{"timezone":"UTC","mode":"immediate"}},"language":"en-US"},"secrets":{"token":"PRIVATE-APP-SECRET"}}`
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
	ruleBody := `{"name":"Routed","config":{"events":["container.oom"]}}`
	res := request("POST", "/notifications/rules", ruleBody, true)
	if res.Code != 200 {
		t.Fatal("standalone rule failed", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), `"enabled"`) {
		t.Fatal("rule response still exposes an enabled state")
	}
	legacyEnable := strings.Replace(ruleBody, `"name":"Routed"`, `"name":"Routed","enabled":false`, 1)
	if response := request("POST", "/notifications/rules", legacyEnable, true); response.Code != http.StatusBadRequest {
		t.Fatal("removed rule enable field accepted", response.Code)
	}
	var savedRule struct{ Data notification.Rule }
	json.Unmarshal(res.Body.Bytes(), &savedRule)
	application.Data.Config.Subscription.RuleIDs = []string{savedRule.Data.ID}
	valid := notification.ChannelInput{Name: application.Data.Name, Provider: application.Data.Provider, Enabled: true, Version: application.Data.Version, Config: application.Data.Config}
	payload, _ := json.Marshal(valid)
	res = request("PUT", path, string(payload), true)
	if res.Code != 200 {
		t.Fatal("subscription save failed", res.Code, res.Body.String())
	}
	json.Unmarshal(res.Body.Bytes(), &application)
	valid.Version = application.Data.Version
	valid.Config.Subscription.RuleIDs = []string{"missing-rule"}
	payload, _ = json.Marshal(valid)
	if res := request("PUT", path, string(payload), true); res.Code != 422 {
		t.Fatal("missing subscription rule accepted", res.Code)
	}
	// Rule writes cannot accidentally carry the old routing/delivery fields.
	for _, field := range []string{`"channel_ids":[]`, `"mode":"merge"`, `"recovery":true`} {
		legacy := strings.Replace(ruleBody, `"events":["container.oom"]`, `"events":["container.oom"],`+field, 1)
		if res := request("POST", "/notifications/rules", legacy, true); res.Code != http.StatusBadRequest {
			t.Fatal("legacy rule config accepted", field, res.Code)
		}
	}
	if err := notify.Ingest(context.Background(), event.Event{ID: "http-overlap", Type: "container.oom", ResourceID: "http-overlap"}); err != nil {
		t.Fatal(err)
	}
	res = request("GET", "/notifications/deliveries", "", true)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"matched_rules"`) || strings.Contains(res.Body.String(), `"rule_id"`) || strings.Contains(res.Body.String(), `"snapshot_json"`) {
		t.Fatal("invalid delivery contract", res.Code, res.Body.String())
	}
	var deliveries struct{ Data []notification.Delivery }
	if err := json.Unmarshal(res.Body.Bytes(), &deliveries); err != nil || len(deliveries.Data) != 2 {
		t.Fatal("selected rule did not take effect without an enabled field", err)
	}
	for _, delivery := range deliveries.Data {
		if len(delivery.MatchedRules) != 1 || delivery.MatchedRules[0].ID != savedRule.Data.ID {
			t.Fatal("subscription did not apply the selected rule to both conversations")
		}
	}
	// Deleting a rule detaches defaults and makes stale channel writes conflict.
	beforeDelete := application.Data.Version
	if res := request("DELETE", "/notifications/rules/"+savedRule.Data.ID, "", true); res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	current, err := notify.Channel(context.Background(), application.Data.ID)
	if err != nil || current.Version != beforeDelete+1 || len(current.Config.Subscription.RuleIDs) != 0 {
		t.Fatal("delete did not update binding version", current, err)
	}
	stale := notification.ChannelInput{Name: application.Data.Name, Provider: application.Data.Provider, Enabled: true, Version: beforeDelete, Config: current.Config}
	payload, _ = json.Marshal(stale)
	if res := request("PUT", path, string(payload), true); res.Code != 409 {
		t.Fatal("stale channel write accepted", res.Code)
	}
	application.Data = current
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
	removedConfig := strings.Replace(create, `"language":"en-US"`, `"interactive":true,"language":"en-US"`, 1)
	if res := request("POST", "/notifications/channels", removedConfig, true); res.Code != http.StatusBadRequest {
		t.Fatal("removed channel config accepted", res.Code)
	}
	telegram := `{"name":"Telegram","provider":"telegram","enabled":false,"config":{"subscription":{"rule_ids":[],"policy":{"timezone":"UTC","mode":"immediate"}},"language":"en-US"},"secrets":{"token":"123:PRIVATE-TOKEN"}}`
	for _, enabled := range []bool{false, true} {
		body := telegram
		if enabled {
			body = strings.Replace(body, `"language":"en-US"`, `"auto_discover":true,"language":"en-US"`, 1)
		}
		res := request("POST", "/notifications/channels", body, true)
		if res.Code != 200 || !strings.Contains(res.Body.String(), `"auto_discover":`+fmt.Sprint(enabled)) || strings.Contains(res.Body.String(), "PRIVATE-TOKEN") {
			t.Fatal("invalid Telegram discovery config", res.Code, res.Body.String())
		}
	}
}
