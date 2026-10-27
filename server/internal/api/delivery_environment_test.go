package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/cd"
	gitrepo "github.com/suma/suma/server/internal/git"
)

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
