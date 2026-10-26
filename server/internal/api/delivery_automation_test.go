package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func TestDeliveryAutomationHTTP(t *testing.T) {
	h := newDeliveryCreationHarness(t)
	if result := h.request(http.MethodPost, "/api/v1/delivery-projects", deliveryCreationBody("switch")); result.Code != http.StatusCreated {
		t.Fatal("create failed", result.Code)
	}
	path := "/api/v1/delivery-projects/switch/automation"
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"false"}`} {
		if result := h.request(http.MethodPut, path, []byte(body)); result.Code != http.StatusBadRequest {
			t.Fatal("invalid switch accepted", body, result.Code)
		}
	}
	for _, enabled := range []bool{false, true} {
		body, _ := json.Marshal(map[string]bool{"enabled": enabled})
		result := h.request(http.MethodPut, path, body)
		var response struct {
			Data struct {
				Enabled bool `json:"automation_enabled"`
			} `json:"data"`
		}
		if result.Code != http.StatusOK || json.Unmarshal(result.Body.Bytes(), &response) != nil || response.Data.Enabled != enabled {
			t.Fatal("switch failed", result.Code)
		}
		var project database.DeliveryProject
		if err := h.db.Where("name = ?", "switch").First(&project).Error; err != nil || project.AutomationEnabled != enabled || !project.WebhookEnabled || project.ReconcileMode != "manual" {
			t.Fatal("switch changed configuration", err)
		}
	}
	var count int64
	h.db.Model(&database.AuditLog{}).Where("action IN ?", []string{"cd.automation.pause", "cd.automation.resume"}).Count(&count)
	if count != 2 {
		t.Fatal("missing switch audit", count)
	}
	h.db.Model(&database.Task{}).Count(&count)
	if count != 0 {
		t.Fatal("switch queued delivery")
	}
	if result := h.request(http.MethodPut, "/api/v1/delivery-projects/missing/automation", []byte(`{"enabled":false}`)); result.Code != http.StatusNotFound {
		t.Fatal("missing project status", result.Code)
	}
	h.cookie = nil
	if result := h.request(http.MethodPut, path, []byte(`{"enabled":false}`)); result.Code != http.StatusUnauthorized {
		t.Fatal("switch accepted without authentication", result.Code)
	}
}

func TestPausedDeliveryWebhookHTTPReturnsIgnoredWithoutQueuing(t *testing.T) {
	h := newDeliveryCreationHarness(t)
	created := h.request(http.MethodPost, "/api/v1/delivery-projects", deliveryCreationBody("paused-hook"))
	var response struct {
		Data struct {
			Configuration struct {
				Secret string `json:"webhook_secret"`
				ID     string `json:"webhook_id"`
			} `json:"configuration"`
		} `json:"data"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &response) != nil || response.Data.Configuration.Secret == "" {
		t.Fatal("webhook setup failed")
	}
	if result := h.request(http.MethodPut, "/api/v1/delivery-projects/paused-hook/automation", []byte(`{"enabled":false}`)); result.Code != http.StatusOK {
		t.Fatal("pause failed")
	}
	for _, valid := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/git/"+response.Data.Configuration.ID, bytes.NewBufferString(`{"repository":"https://git.example.test/team/deploy.git","ref":"refs/heads/main"}`))
		value := "invalid"
		if valid {
			value = response.Data.Configuration.Secret
		}
		request.Header.Set("Authorization", "Bearer "+value)
		result := httptest.NewRecorder()
		h.router.ServeHTTP(result, request)
		if !valid && result.Code != http.StatusUnauthorized {
			t.Fatal("signature bypassed", result.Code)
		}
		if valid && (result.Code != http.StatusOK || !bytes.Contains(result.Body.Bytes(), []byte(`"ignored":true`))) {
			t.Fatal("paused webhook did not acknowledge ignore", result.Code)
		}
	}
	for _, model := range []any{&database.Task{}, &database.GitWebhookDelivery{}} {
		var count int64
		h.db.Model(model).Count(&count)
		if count != 0 {
			t.Fatal("paused webhook created task or delivery", count)
		}
	}
}
