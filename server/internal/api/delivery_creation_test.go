package api

import (
	"context"
	"encoding/json"
	"github.com/suma/suma/server/internal/testutil"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

func newDeliveryCreationHarness(t *testing.T) projectHTTPHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	credentials := gitrepo.NewCredentialService(db, store)
	delivery := cd.NewService(db, nil, credentials, nil, task.NewService(db), nil, store)
	users := auth.NewService(db, time.Hour)
	if _, err := users.Initialize(context.Background(), "admin", "admin@example.test", "", "test-password-long"); err != nil {
		t.Fatal(err)
	}
	token, _, err := users.Login(context.Background(), "admin", "test-password-long", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Auth: users, CD: delivery, Audit: audit.NewService(db)})
	return projectHTTPHarness{router: router, cookie: &http.Cookie{Name: sessionCookie, Value: token}, db: db}
}

func deliveryCreationBody(name string) []byte {
	body, _ := json.Marshal(map[string]any{
		"name": name,
		"configuration": cd.ConfigureInput{
			Repository: gitrepo.Repository{CloneURL: "https://git.example.test/team/deploy.git", RefType: gitrepo.RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}, Authentication: gitrepo.Authentication{Source: gitrepo.CredentialSourceProject, Credential: &gitrepo.CredentialInput{Name: "private-git", AuthType: gitrepo.AuthHTTPToken, Secret: "test-only-private-token"}}},
			NodeIDs:    []string{"local"}, ReconcileMode: cd.ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 120, WebhookEnabled: true,
		},
	})
	return body
}

func TestDeliveryFullConfigurationCreationHTTP(t *testing.T) {
	h := newDeliveryCreationHarness(t)
	result := h.request(http.MethodPost, "/api/v1/delivery-projects", deliveryCreationBody("complete-new"))
	if result.Code != http.StatusCreated {
		t.Fatalf("creation returned %d", result.Code)
	}
	var response struct {
		Data struct {
			cd.Project
			Configuration cd.Configuration `json:"configuration"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil || !response.Data.Configured || !response.Data.Configuration.Configured {
		t.Fatal("creation did not return a configured project")
	}
	if strings.Contains(result.Body.String(), "test-only-private-token") || response.Data.Configuration.Repository.Authentication.Credential != nil {
		t.Fatal("credential secret was exposed by creation")
	}
	if response.Data.Configuration.WebhookSecret == "" {
		t.Fatal("generated webhook secret was not returned")
	}
	for _, path := range []string{"/api/v1/delivery-projects", "/api/v1/delivery-projects/complete-new/configuration"} {
		loaded := h.request(http.MethodGet, path, nil)
		if loaded.Code != http.StatusOK || strings.Contains(loaded.Body.String(), response.Data.Configuration.WebhookSecret) || strings.Contains(loaded.Body.String(), "test-only-private-token") {
			t.Fatal("GET exposed a one-time or credential secret")
		}
	}
	duplicate := h.request(http.MethodPost, "/api/v1/delivery-projects", deliveryCreationBody("complete-new"))
	if duplicate.Code != http.StatusConflict {
		t.Fatal("duplicate creation was accepted")
	}
	var count int64
	h.db.Model(&database.DeliveryProject{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate request changed project count")
	}
}

func TestDeliveryCreationFailureAndLegacyHTTP(t *testing.T) {
	h := newDeliveryCreationHarness(t)
	body := strings.ReplaceAll(string(deliveryCreationBody("invalid-new")), "compose.yml", "../outside.yml")
	if result := h.request(http.MethodPost, "/api/v1/delivery-projects", []byte(body)); result.Code != http.StatusConflict {
		t.Fatalf("invalid configuration returned %d", result.Code)
	}
	var count int64
	h.db.Model(&database.DeliveryProject{}).Count(&count)
	if count != 0 {
		t.Fatal("validation failure left an empty project")
	}
	legacy := h.request(http.MethodPost, "/api/v1/delivery-projects", []byte(`{"name":"legacy-empty","node_ids":["local"]}`))
	if legacy.Code != http.StatusCreated {
		t.Fatalf("legacy creation returned %d", legacy.Code)
	}
	unauthenticated := h
	unauthenticated.cookie = &http.Cookie{Name: sessionCookie, Value: "invalid-session"}
	if result := unauthenticated.request(http.MethodPost, "/api/v1/delivery-projects", deliveryCreationBody("unauthenticated")); result.Code != http.StatusUnauthorized {
		t.Fatal("creation bypassed session authentication")
	}
}
