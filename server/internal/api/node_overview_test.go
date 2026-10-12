package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/testutil"
)

func overviewHTTPFixture(t *testing.T, handler http.Handler) (*gin.Engine, *node.Service, func(context.Context, string, bool) *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := httptest.NewServer(handler)
	t.Cleanup(engine.Close)
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, store, "unix:///unused.sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodes.Close() })
	if err := db.Model(&database.Node{}).Where("id = ?", "local").Updates(map[string]any{"connection_type": "tcp", "endpoint": strings.Replace(engine.URL, "http://", "tcp://", 1), "tls_mode": "disabled"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Node{ID: "disabled", Name: "Disabled", ConnectionType: "unix", Endpoint: "unix:///unused.sock", TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(db, time.Hour)
	if _, err := authService.Initialize(context.Background(), "admin", "admin@example.test", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authService.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Nodes: nodes, Auth: authService, Audit: audit.NewService(db)})
	return router, nodes, func(ctx context.Context, path string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		if authenticated {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
}

func overviewEngineJSON(w http.ResponseWriter, r *http.Request, path string) {
	var value any
	switch path {
	case "/_ping":
		w.Header().Set("Api-Version", "1.44")
		_, _ = w.Write([]byte("OK"))
		return
	case "/info":
		value = map[string]any{"ID": "overview-engine", "Name": "overview-host", "ServerVersion": "28.5", "NCPU": 2, "MemTotal": 4096, "ContainersRunning": 1}
	case "/containers/json":
		value = []map[string]any{{"Id": "app", "Names": []string{"/app"}, "Image": "app:latest", "State": "running"}}
	case "/containers/app/stats":
		value = map[string]any{
			"cpu_stats":    map[string]any{"cpu_usage": map[string]any{"total_usage": 300}, "system_cpu_usage": 1000, "online_cpus": 2},
			"precpu_stats": map[string]any{"cpu_usage": map[string]any{"total_usage": 100}, "system_cpu_usage": 500},
			"memory_stats": map[string]any{"usage": 1024, "stats": map[string]any{"inactive_file": 128}},
			"networks":     map[string]any{"eth0": map[string]any{"rx_bytes": 2048, "tx_bytes": 4096}},
		}
	case "/containers/app/json":
		value = map[string]any{"State": map[string]any{"StartedAt": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)}}
	case "/networks":
		value = []map[string]any{{"Id": "network"}}
	case "/volumes":
		value = map[string]any{"Volumes": []map[string]any{{"Name": "volume"}}}
	case "/system/df":
		value = map[string]any{"LayersSize": 20, "Containers": []map[string]any{{"SizeRw": 30}}, "Volumes": []map[string]any{{"UsageData": map[string]any{"Size": 40}}}, "BuildCache": []map[string]any{{"Size": 10}}}
	default:
		http.NotFound(w, r)
		return
	}
	writeProjectDockerJSON(w, value)
}

func TestNodeOverviewProfilesAndAuthentication(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	statsFail := false
	_, _, request := overviewHTTPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := projectDockerVersion.ReplaceAllString(r.URL.Path, "")
		mu.Lock()
		requests[path]++
		fail := statsFail
		mu.Unlock()
		if fail && path == "/containers/app/stats" {
			http.Error(w, "stats unavailable", http.StatusInternalServerError)
			return
		}
		overviewEngineJSON(w, r, path)
	}))
	ctx := context.Background()
	for _, suffix := range []string{"info", "metrics", "resources", "disk"} {
		if got := request(ctx, "/api/v1/nodes/local/overview/"+suffix, false); got.Code != 401 {
			t.Fatalf("unauthenticated %s = %d", suffix, got.Code)
		}
	}
	if got := request(ctx, "/api/v1/nodes", true); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if got := request(ctx, "/api/v1/nodes/local/overview/metrics?size=invalid", true); got.Code != 400 {
		t.Fatalf("invalid size = %d", got.Code)
	}
	mu.Lock()
	if len(requests) != 0 {
		t.Errorf("metadata/auth/validation unexpectedly read Docker: %v", requests)
	}
	mu.Unlock()
	read := func(suffix string) map[string]any {
		t.Helper()
		response := request(ctx, "/api/v1/nodes/local/overview/"+suffix, true)
		if response.Code != 200 {
			t.Fatalf("%s = %d %s", suffix, response.Code, response.Body.String())
		}
		var result struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Data
	}
	small := read("metrics?size=small")
	if small["metrics_available"] != true || small["container_cpu_percent"] != float64(80) || small["container_memory_bytes"] != float64(896) || len(small) != 3 {
		t.Fatalf("small profile = %#v", small)
	}
	mu.Lock()
	if requests["/containers/app/json"] != 0 || requests["/system/df"] != 0 || requests["/networks"] != 0 {
		t.Errorf("small metrics requested hidden information: %v", requests)
	}
	mu.Unlock()
	medium := read("metrics?size=medium")
	row := medium["containers"].([]any)[0].(map[string]any)
	if len(row) != 7 || medium["container_network_rx_bytes"] != float64(2048) || medium["longest_container_uptime_seconds"].(float64) < 59 {
		t.Fatalf("medium profile = %#v", medium)
	}
	large := read("metrics?size=large")
	row = large["containers"].([]any)[0].(map[string]any)
	if len(row) != 12 || row["network_rx_bytes"] != float64(2048) || row["uptime_seconds"].(float64) < 59 {
		t.Fatalf("large profile = %#v", large)
	}
	if counts := read("resources"); counts["networks"] != float64(1) || counts["volumes"] != float64(1) {
		t.Fatalf("counts = %#v", counts)
	}
	if disk := read("disk"); disk["docker_disk_usage_bytes"] != float64(100) {
		t.Fatalf("disk = %#v", disk)
	}
	mu.Lock()
	statsFail = true
	mu.Unlock()
	if failed := read("metrics?size=small"); failed["metrics_available"] != false {
		t.Fatalf("failed sample reported available: %#v", failed)
	}
	if got := request(ctx, "/api/v1/nodes/missing/overview/info", true); got.Code != 404 {
		t.Fatalf("missing node = %d", got.Code)
	}
	if got := request(ctx, "/api/v1/nodes/disabled/overview/metrics", true); got.Code != 503 {
		t.Fatalf("disabled node = %d", got.Code)
	}
}

func TestNodeOverviewReadsAreIndependentAndCancelable(t *testing.T) {
	statsStarted, diskStarted, canceled := make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 2)
	_, _, request := overviewHTTPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := projectDockerVersion.ReplaceAllString(r.URL.Path, "")
		switch path {
		case "/containers/app/stats", "/system/df":
			if path == "/system/df" {
				diskStarted <- struct{}{}
			} else {
				statsStarted <- struct{}{}
			}
			<-r.Context().Done()
			canceled <- struct{}{}
		default:
			overviewEngineJSON(w, r, path)
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{}, 2)
	for _, suffix := range []string{"metrics?size=small", "disk"} {
		go func(suffix string) { request(ctx, "/api/v1/nodes/local/overview/"+suffix, true); done <- struct{}{} }(suffix)
	}
	for _, started := range []chan struct{}{statsStarted, diskStarted} {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("detail read did not start")
		}
	}
	infoDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { infoDone <- request(ctx, "/api/v1/nodes/local/overview/info", true) }()
	select {
	case info := <-infoDone:
		if info.Code != 200 || !strings.Contains(info.Body.String(), "overview-host") {
			t.Fatalf("info = %d %s", info.Code, info.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("engine information waited for container sampling or disk accounting")
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("Docker read was not canceled")
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("HTTP handler did not finish")
		}
	}
}
