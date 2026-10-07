package api

import (
	"context"
	"encoding/json"
	"github.com/suma/suma/server/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/config"
	"github.com/suma/suma/server/internal/settings"
	"github.com/suma/suma/server/internal/task"
)

func TestSettingsTimezoneHTTP(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	authService := auth.NewService(db, time.Hour)
	if _, err := authService.Initialize(ctx, "admin", "admin@example.test", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authService.Login(ctx, "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	service := settings.NewService(db, config.Config{})
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Auth: authService, Audit: audit.NewService(db), Tasks: task.NewService(db), Settings: service})
	request := func(method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/settings", strings.NewReader(body))
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	for _, zone := range []string{"Asia/Shanghai", "America/New_York", "system"} {
		payload, _ := json.Marshal(map[string]string{"general.timezone": zone})
		if response := request(http.MethodPut, string(payload)); response.Code != http.StatusOK {
			t.Fatalf("save %s: %d %s", zone, response.Code, response.Body.String())
		}
		response := request(http.MethodGet, "")
		var result struct {
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || result.Data["general.timezone"] != zone {
			t.Fatalf("read saved timezone: %d %s, %v", response.Code, response.Body.String(), err)
		}
	}
	if response := request(http.MethodPut, `{"general.timezone":"Not/AZone","general.server_name":"invalid"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid zone accepted: %d %s", response.Code, response.Body.String())
	}
	stored, err := service.Get(ctx)
	if err != nil || stored["general.timezone"] != "system" || stored["general.server_name"] != "SUMA" {
		t.Fatalf("rejected update changed settings: %#v, %v", stored, err)
	}
}
