package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/credential"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/projectcd"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

func TestProjectCDHTTPModeIsolationAndMaskedEnvironment(t *testing.T) {
	h := newProjectHTTPHarness(t)
	created := h.request(http.MethodPost, "/api/v1/nodes/local/projects", []byte(`{"name":"single","compose":"services: {app: {image: busybox:stable}}","environment":""}`))
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(h.db, store, "unix:///fixture.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	runner, err := compose.NewRunner("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	service := projectcd.New(h.db, nil, gitrepo.NewCredentialService(h.db, store), credential.NewRegistryService(h.db, store), runner, task.NewService(h.db), audit.NewService(h.db), store, projectcd.Dependencies{Project: func(context.Context, string) (*compose.Service, error) { return h.compose, nil }, Target: func(context.Context, string) (compose.Target, error) {
		return compose.Target{NodeID: "local", RuntimeIdentity: "fixture"}, nil
	}})
	registerProjectCDRoutes(h.router.Group("/api/v1"), Dependencies{ProjectCD: service, Nodes: nodes, Auth: auth.NewService(h.db, time.Hour), Audit: audit.NewService(h.db)})
	path := "/api/v1/nodes/local/projects/compose/single/gitops"
	input := `{"repository":{"clone_url":"https://git.example.test/app.git","ref_type":"branch","ref":"main","compose_files":["compose.yml"],"authentication":{"source":"none"}},"environment":[{"name":"API_TOKEN","value":"private-http-value"}]}`
	saved := h.request(http.MethodPut, path, []byte(input))
	if saved.Code != 200 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	if strings.Contains(saved.Body.String(), "private-http-value") {
		t.Fatal("configuration returned secret")
	}
	stale := h.request(http.MethodPut, path, []byte(input))
	if stale.Code != 409 {
		t.Fatal("stale write accepted", stale.Code)
	}
	old := h.request(http.MethodPut, strings.TrimSuffix(path, "/gitops"), []byte(`{"compose":"services: {app: {image: altered}}","environment":""}`))
	if old.Code != 409 {
		t.Fatal("old route bypassed CD ownership", old.Code)
	}
	values := h.request(http.MethodGet, path+"/environment/values", nil)
	if values.Code != 200 || !strings.Contains(values.Body.String(), "private-http-value") {
		t.Fatal("explicit reveal failed")
	}
	var response struct {
		Data struct {
			Revision uint64 `json:"revision"`
		}
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	auto := h.request(http.MethodPut, path+"/automation", []byte(`{"enabled":true,"expected_revision":1}`))
	if auto.Code != 422 {
		t.Fatal("first application gate bypassed", auto.Code)
	}
	h.cookie = nil
	if result := h.request(http.MethodGet, path, nil); result.Code != 401 {
		t.Fatal("unauthenticated configuration readable", result.Code)
	}
}
