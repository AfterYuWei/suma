package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/cleanup"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

type httpCleanupRuntime struct{}

func (httpCleanupRuntime) CleanupCapabilities(context.Context) (cleanup.Capabilities, error) {
	return cleanup.Capabilities{Available: true, BuildCache: true}, nil
}
func (httpCleanupRuntime) CleanupInventory(context.Context) (cleanup.Inventory, error) {
	return cleanup.Inventory{Resources: []cleanup.Resource{}, Capabilities: cleanup.Capabilities{Available: true}}, nil
}
func (httpCleanupRuntime) CleanupResource(context.Context, cleanup.Kind, string) (cleanup.Resource, error) {
	return cleanup.Resource{}, cleanup.ErrGone
}
func (httpCleanupRuntime) CleanupRemove(context.Context, cleanup.Kind, string) error { return nil }
func (httpCleanupRuntime) CleanupPruneCache(context.Context, cleanup.CacheOptions) (cleanup.CacheReport, error) {
	return cleanup.CacheReport{}, nil
}
func TestCleanupHTTPAuthorizationValidationAndIsolation(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, store, "unix:///unused.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	if err = db.Create(&database.Node{ID: "edge", Name: "Edge", ConnectionType: "unix", Endpoint: "unix:///edge.sock", TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(db, time.Hour)
	if _, err = authentication.Initialize(context.Background(), "admin", "admin@example.test", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authentication.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	audits := audit.NewService(db)
	service := cleanup.NewService(db, tasks, audits, cleanup.Dependencies{Node: func(ctx context.Context, id string) (cleanup.Node, error) {
		n, e := nodes.Get(ctx, id)
		return cleanup.Node{ID: n.ID, Name: n.Name, Enabled: n.Enabled}, e
	}, Runtime: func(context.Context, string) (cleanup.Runtime, error) { return httpCleanupRuntime{}, nil }, Protection: func(context.Context, string) (cleanup.Protection, error) { return cleanup.Protection{}, nil }})
	defer service.Stop()
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Nodes: nodes, Auth: authentication, Tasks: tasks, Audit: audits, Cleanup: service})
	request := func(method, path, body, origin string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if authenticated {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	path := "/api/v1/nodes/edge/cleanup"
	for _, endpoint := range []string{"/policy", "/runs", "/runs/missing"} {
		if out := request("GET", path+endpoint, "", "", false); out.Code != 401 {
			t.Fatalf("anonymous %s: %d", endpoint, out.Code)
		}
	}
	if out := request("POST", path+"/preview", "{}", "http://evil.test", true); out.Code != 403 {
		t.Fatal("cross-origin write accepted", out.Code)
	}
	if out := request("GET", "/api/v1/nodes/missing/cleanup/policy", "", "", true); out.Code != 404 {
		t.Fatal("unknown node", out.Code)
	}
	in := cleanup.Update{Config: cleanup.DefaultConfig("UTC")}
	in.Enabled = true
	body, _ := json.Marshal(in)
	if out := request("PUT", path+"/policy", string(body), "http://example.com", true); out.Code != 422 {
		t.Fatal("unconfirmed grant", out.Code, out.Body.String())
	}
	in.ConfirmationName = "Edge"
	in.Authorize = true
	body, _ = json.Marshal(in)
	out := request("PUT", path+"/policy", string(body), "http://example.com", true)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	if out = request("PUT", path+"/policy", string(body), "http://example.com", true); out.Code != 409 {
		t.Fatal("stale policy accepted", out.Code)
	}
	if out = request("PUT", path+"/policy", `{"volume_auto_delete":true}`, "http://example.com", true); out.Code != 400 {
		t.Fatal("unknown destructive field accepted", out.Code)
	}
	out = request("POST", path+"/preview", "{}", "http://example.com", true)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	var payload struct {
		Data cleanup.Preview `json:"data"`
	}
	if err = json.Unmarshal(out.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	runBody := `{"preview_id":"` + payload.Data.ID + `","confirmation_name":"Edge"}`
	if out = request("POST", path+"/run", `{"preview_id":"`+payload.Data.ID+`","confirmation_name":"wrong"}`, "http://example.com", true); out.Code != 422 {
		t.Fatal("node confirmation ignored", out.Code)
	}
	if out = request("POST", "/api/v1/nodes/local/cleanup/run", `{"preview_id":"`+payload.Data.ID+`","confirmation_name":"Local"}`, "http://example.com", true); out.Code != 409 {
		t.Fatal("cross-node preview accepted", out.Code, out.Body.String())
	}
	out = request("POST", path+"/run", runBody, "http://example.com", true)
	if out.Code != 202 {
		t.Fatal(out.Code, out.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	var run database.CleanupRun
	for time.Now().Before(deadline) {
		db.Where("node_id = ?", "edge").Order("created_at DESC").First(&run)
		if run.Status == "success" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if run.Status != "success" {
		t.Fatal(run.Status)
	}
	if out = request("GET", "/api/v1/nodes/local/cleanup/runs/"+run.ID, "", "", true); out.Code != 404 {
		t.Fatal("cross-node run exposed", out.Code)
	}
	if out = request("GET", path+"/runs/"+run.ID, "", "", true); out.Code != 200 {
		t.Fatal(out.Code)
	}
	var auditRows []database.AuditLog
	db.Where("action LIKE ?", "cleanup.%").Find(&auditRows)
	if len(auditRows) < 4 {
		t.Fatal("missing authorization/start/final audits", auditRows)
	}
	for _, row := range auditRows {
		if row.NodeID != "edge" {
			t.Fatal("audit missing node scope")
		}
	}
	for _, legacy := range []string{"/api/v1/nodes/edge/system/prune", "/api/v1/system/prune"} {
		if out = request("POST", legacy, `{"confirm":"wrong"}`, "http://example.com", true); out.Code != 400 {
			t.Fatalf("legacy confirmation bypass: %d", out.Code)
		}
		if out = request("POST", legacy, `{"confirm":"PRUNE"}`, "http://example.com", true); out.Code != 202 {
			t.Fatalf("legacy cleanup did not use shared service: %d %s", out.Code, out.Body.String())
		}
		var accepted struct {
			Data database.Task `json:"data"`
		}
		if err = json.Unmarshal(out.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		// Wait for the Task service's final log as well as the cleanup callback;
		// its final persistence happens after the domain callback returns.
		complete := false
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var finished database.Task
			var terminalLogs int64
			db.First(&finished, "id = ?", accepted.Data.ID)
			if finished.Status == task.StatusSuccess {
				db.Model(&database.TaskLog{}).Where("task_id = ? AND message = ?", finished.ID, finished.Message).Count(&terminalLogs)
				if terminalLogs > 0 {
					complete = true
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
		if !complete {
			t.Fatal("legacy cleanup task did not finish persistence")
		}
	}
}
