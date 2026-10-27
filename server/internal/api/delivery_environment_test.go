package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
)

func TestDeliveryEnvironmentReadRequiresSessionWithoutDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Auth: auth.NewService(nil, time.Hour)})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/delivery-projects/app/environment", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated read reached the environment service")
	}
}

func TestDeliveryEnvironmentExplicitReadRequiresSessionAndDoesNotCacheValues(t *testing.T) {
	h := newDeliveryCreationHarness(t)
	if result := h.request(http.MethodPost, "/api/v1/delivery-projects", deliveryCreationBody("environment-read")); result.Code != http.StatusCreated {
		t.Fatal("creation failed", result.Code)
	}
	path := "/api/v1/delivery-projects/environment-read/environment"
	unauthorized := h
	unauthorized.cookie = nil
	if result := unauthorized.request(http.MethodGet, path, nil); result.Code != http.StatusUnauthorized || strings.Contains(result.Body.String(), "test-only-manual-environment") {
		t.Fatal("explicit environment read did not require authentication")
	}
	var before database.DeliveryProject
	h.db.Where("name = ?", "environment-read").First(&before)
	result := h.request(http.MethodGet, path, nil)
	var response struct {
		Data map[string]string `json:"data"`
	}
	if result.Code != http.StatusOK || result.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(result.Body.Bytes(), &response) != nil || response.Data["PASSWORD"] != "test-only-manual-environment" {
		t.Fatal("explicit read did not return saved values without caching")
	}
	if strings.Contains(result.Body.String(), "test-only-private-token") || strings.Contains(result.Body.String(), "webhook_secret") {
		t.Fatal("environment read exposed unrelated secrets")
	}
	var after database.DeliveryProject
	h.db.First(&after, before.ID)
	if string(before.EnvironmentCiphertext) != string(after.EnvironmentCiphertext) || !before.UpdatedAt.Equal(after.UpdatedAt) {
		t.Fatal("viewing values modified the project")
	}
	var tasks int64
	h.db.Model(&database.Task{}).Count(&tasks)
	if tasks != 0 {
		t.Fatal("viewing values queued a task")
	}
	var audits []database.AuditLog
	h.db.Find(&audits)
	raw, _ := json.Marshal(audits)
	if !strings.Contains(string(raw), "cd.environment.read") || strings.Contains(string(raw), "test-only-manual-environment") || strings.Contains(string(raw), "test-only-private-token") {
		t.Fatal("read audit missing or exposed a secret")
	}
	if missing := h.request(http.MethodGet, "/api/v1/delivery-projects/missing/environment", nil); missing.Code != http.StatusNotFound || missing.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unknown project read did not return a non-cached 404")
	}
	h.db.Model(&after).Update("environment_ciphertext", []byte("test-only-corrupt-ciphertext"))
	if failed := h.request(http.MethodGet, path, nil); failed.Code != http.StatusInternalServerError || failed.Header().Get("Cache-Control") != "no-store" || strings.Contains(failed.Body.String(), "test-only") {
		t.Fatal("decryption failure did not return a generic non-cached error")
	}
}

func TestDeliveryEnvironmentHTTPPreserveReplaceClearAndValidation(t *testing.T) {
	h := newDeliveryCreationHarness(t)
	if response := h.request(http.MethodPost, "/api/v1/delivery-projects", deliveryCreationBody("environment")); response.Code != http.StatusCreated {
		t.Fatal("creation failed", response.Code)
	}
	path := "/api/v1/delivery-projects/environment/configuration"
	input := cd.ConfigureInput{Repository: gitrepo.Repository{CloneURL: "https://git.example.test/app.git", RefType: gitrepo.RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}, Authentication: gitrepo.Authentication{Source: gitrepo.CredentialSourceNone}}, ReconcileMode: cd.ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 60}
	for _, variables := range [][]cd.EnvironmentVariable{
		nil, // Omission/null from older clients preserves values.
		{{Name: "PASSWORD"}},
		{{Name: "PASSWORD", Value: testDeliveryEnvironmentValue("new-test-private-value")}},
		{{Name: "PASSWORD", Value: testDeliveryEnvironmentValue("")}},
		{},
	} {
		input.Environment = variables
		raw, _ := json.Marshal(input)
		response := h.request(http.MethodPut, path, raw)
		if response.Code != http.StatusOK {
			t.Fatal("update failed", response.Code)
		}
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			var body []byte
			if method == http.MethodPut {
				body = raw
			}
			result := h.request(method, path, body)
			if strings.Contains(result.Body.String(), "test-only-manual-environment") || strings.Contains(result.Body.String(), "new-test-private-value") {
				t.Fatal("HTTP returned a manual value")
			}
		}
	}
	for _, name := range []string{"1BAD", "DOCKER_HOST", "COMPOSE_FILE"} {
		input.Environment = []cd.EnvironmentVariable{{Name: name, Value: testDeliveryEnvironmentValue("test-only-private-error")}}
		raw, _ := json.Marshal(input)
		result := h.request(http.MethodPut, path, raw)
		if result.Code != http.StatusUnprocessableEntity || strings.Contains(result.Body.String(), "test-only-private-error") {
			t.Fatal("unsafe variable accepted or value leaked")
		}
	}
	unauthorized := h
	unauthorized.cookie = nil
	if result := unauthorized.request(http.MethodGet, path, nil); result.Code != http.StatusUnauthorized {
		t.Fatal("environment read did not require authentication")
	}
}
