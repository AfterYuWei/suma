package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/node"
)

func TestContainerRuntimeAuthenticationIsolationAndPartialFailure(t *testing.T) {
	var inspected atomic.Int32
	engine := func(id string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			path := projectDockerVersion.ReplaceAllString(r.URL.Path, "")
			switch path {
			case "/info":
				writeProjectDockerJSON(w, map[string]any{"ID": id, "Name": id, "ServerVersion": "28.5"})
			case "/containers/json":
				if r.URL.Query().Get("all") != "1" {
					t.Error("runtime metadata excluded stopped containers")
				}
				writeProjectDockerJSON(w, []map[string]any{{"Id": id}, {"Id": "missing"}})
			case "/containers/" + id + "/json":
				inspected.Add(1)
				writeProjectDockerJSON(w, map[string]any{"State": map[string]any{"Status": "running", "Health": map[string]any{"Status": "unhealthy"}}, "Config": map[string]any{}, "RestartCount": 4, "HostConfig": map[string]any{"Memory": 536870912}})
			case "/containers/missing/json":
				http.Error(w, "gone", 404)
			default:
				overviewEngineJSON(w, r, path)
			}
		}
	}
	_, nodes, request := overviewHTTPFixture(t, engine("primary"))
	ctx := context.Background()
	for _, check := range []struct {
		path string
		auth bool
		code int
	}{
		{"/api/v1/nodes/local/containers/runtime", false, 401},
		{"/api/v1/nodes/missing/containers/runtime", true, 404},
		{"/api/v1/nodes/disabled/containers/runtime", true, 503},
	} {
		if response := request(ctx, check.path, check.auth); response.Code != check.code {
			t.Fatalf("%s = %d %s", check.path, response.Code, response.Body.String())
		}
	}
	if inspected.Load() != 0 {
		t.Fatal("authentication/node validation accessed Docker")
	}
	second := httptest.NewServer(engine("secondary"))
	defer second.Close()
	endpoint := strings.Replace(second.URL, "http://", "tcp://", 1)
	other, err := nodes.Create(ctx, node.Input{Name: "Secondary", ConnectionType: node.ConnectionTCP, Endpoint: endpoint, TLSMode: node.TLSDisabled, PlaintextConfirmation: "127.0.0.1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ nodeID, engineID string }{{"local", "primary"}, {other.ID, "secondary"}} {
		response := request(ctx, "/api/v1/nodes/"+check.nodeID+"/containers/runtime", true)
		if response.Code != 200 {
			t.Fatalf("runtime = %d %s", response.Code, response.Body.String())
		}
		var result struct {
			Data []containerdomain.RuntimeMetadata `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Data) != 2 || result.Data[0].ID != check.engineID {
			t.Fatalf("node runtime leaked: %#v", result.Data)
		}
		value := result.Data[0]
		if value.HealthStatus == nil || *value.HealthStatus != "unhealthy" || value.RestartCount == nil || *value.RestartCount != 4 || value.MemoryLimitBytes == nil || *value.MemoryLimitBytes != 536870912 {
			t.Fatalf("unexpected metadata: %#v", value)
		}
		if result.Data[1].HealthStatus != nil || result.Data[1].RestartCount != nil || result.Data[1].MemoryLimitBytes != nil {
			t.Fatal("partial failure fabricated metadata")
		}
	}
}

func TestContainerRuntimeCancelsInspectionWithoutHoldingList(t *testing.T) {
	started, canceled := make(chan struct{}, 1), make(chan struct{}, 1)
	_, _, request := overviewHTTPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := projectDockerVersion.ReplaceAllString(r.URL.Path, "")
		if path == "/containers/app/json" {
			started <- struct{}{}
			<-r.Context().Done()
			canceled <- struct{}{}
			return
		}
		overviewEngineJSON(w, r, path)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request(ctx, "/api/v1/nodes/local/containers/runtime", true) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("inspection did not start")
	}
	if response := request(context.Background(), "/api/v1/nodes/local/containers", true); response.Code != 200 {
		t.Fatal("basic list blocked on metadata")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Engine inspection did not cancel")
	}
	select {
	case response := <-done:
		if response.Code != 503 {
			t.Fatalf("canceled read = %d", response.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime handler did not finish")
	}
}

func TestContainerRuntimeHonorsShorterRequestDeadline(t *testing.T) {
	_, _, request := overviewHTTPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := projectDockerVersion.ReplaceAllString(r.URL.Path, "")
		if path == "/containers/app/json" {
			<-r.Context().Done()
			return
		}
		overviewEngineJSON(w, r, path)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	response := request(ctx, "/api/v1/nodes/local/containers/runtime", true)
	if response.Code != 503 {
		t.Fatalf("timed out read = %d", response.Code)
	}
}
