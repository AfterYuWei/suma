package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
)

func TestProjectConfigurationRevisionHTTP(t *testing.T) {
	h := newProjectHTTPHarness(t)
	created := h.request(http.MethodPost, "/api/v1/nodes/local/projects", []byte(`{"name":"visual-app","backend":"compose","compose":"services: {app: {image: nginx}}\n","environment":"PASSWORD=private-value\n"}`))
	if created.Code != http.StatusCreated {
		t.Fatalf("create %d %s", created.Code, created.Body)
	}
	var response struct {
		Data struct {
			Revision string `json:"revision"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil || response.Data.Revision == "" {
		t.Fatal("missing revision")
	}
	path := "/api/v1/nodes/local/projects/compose/visual-app"
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, image := range []string{"nginx:alpine", "nginx:stable"} {
		wg.Add(1)
		go func(image string) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]string{"compose": "services: {app: {image: " + image + "}}\n", "environment": "PASSWORD=private-value\n", "expected_revision": response.Data.Revision})
			codes <- h.request(http.MethodPut, path, body).Code
		}(image)
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		if code == 200 {
			success++
		}
		if code == 409 {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	stale, _ := json.Marshal(map[string]string{"expected_revision": response.Data.Revision})
	if result := h.request(http.MethodPost, path+"/actions/up", stale); result.Code != http.StatusConflict {
		t.Fatalf("stale deployment %d", result.Code)
	}
	list := h.request(http.MethodGet, "/api/v1/nodes/local/projects", nil)
	if containsSensitiveProjectResponse(list.Body.String(), "private-value") || containsSensitiveProjectResponse(list.Body.String(), response.Data.Revision) {
		t.Fatal("list exposed configuration data")
	}
	legacy := h.request(http.MethodPut, "/api/v1/nodes/local/compose/visual-app", []byte(`{"compose":"services: {app: {image: nginx}}\n","environment":""}`))
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy save %d %s", legacy.Code, legacy.Body)
	}
}
func containsSensitiveProjectResponse(value, secret string) bool {
	for i := 0; i+len(secret) <= len(value); i++ {
		if value[i:i+len(secret)] == secret {
			return true
		}
	}
	return false
}
func TestNewProjectDraftValidationHTTP(t *testing.T) {
	h := newProjectHTTPHarness(t)
	for _, item := range []struct {
		body   string
		status int
	}{
		{`{"name":"newapp","compose":"services: {app: {image: nginx}}\n","environment":""}`, http.StatusOK},
		{`{"name":"newapp","compose":"name: other\nservices: {}\n","environment":""}`, http.StatusUnprocessableEntity},
		{`{"name":"newapp","compose":"services: {app: {image: nginx, volumes: [/var/run/docker.sock:/var/run/docker.sock]}}\n","environment":""}`, http.StatusUnprocessableEntity},
	} {
		result := h.request(http.MethodPost, "/api/v1/nodes/local/projects/validate", []byte(item.body))
		if result.Code != item.status {
			t.Fatalf("validation %d expected %d: %s", result.Code, item.status, result.Body)
		}
	}
	if result := h.request(http.MethodGet, "/api/v1/nodes/local/projects/compose/newapp", nil); result.Code != http.StatusNotFound {
		t.Fatal("validation created a Project")
	}
}
