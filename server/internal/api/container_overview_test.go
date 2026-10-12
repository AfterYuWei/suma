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

	domain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/node"
)

func TestContainerOverviewHTTPAuthenticationValidationAndNodeIsolation(t *testing.T) {
	var reads atomic.Int32
	engine := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			path := projectDockerVersion.ReplaceAllString(r.URL.Path, "")
			switch path {
			case "/containers/app/json":
				reads.Add(1)
				writeProjectDockerJSON(w, map[string]any{"Id": "app", "Name": "/" + name, "Config": map[string]any{"Env": []string{"PASSWORD=http-private"}, "Cmd": []string{"app", "--password", "argument-private"}, "Labels": map[string]string{"app.secret": "label-private"}}, "HostConfig": map[string]any{}, "NetworkSettings": map[string]any{"Networks": map[string]any{name: map[string]string{"IPAddress": "172.20.0.2"}}}, "State": map[string]any{"Status": "running", "Running": true}})
			case "/containers/gone/json":
				http.Error(w, "password=engine-private", 404)
			case "/containers/broken/json":
				http.Error(w, "password=engine-private", 500)
			default:
				overviewEngineJSON(w, r, path)
			}
		}
	}
	_, nodes, request := overviewHTTPFixture(t, engine("primary"))
	for _, c := range []struct {
		path string
		auth bool
		code int
	}{
		{"/api/v1/nodes/local/containers/app/overview", false, 401},
		{"/api/v1/nodes/missing/containers/app/overview", true, 404},
		{"/api/v1/nodes/disabled/containers/app/overview", true, 503},
		{"/api/v1/nodes/local/containers/-invalid/overview", true, 400},
	} {
		if out := request(context.Background(), c.path, c.auth); out.Code != c.code {
			t.Fatalf("unexpected HTTP status %d", out.Code)
		}
	}
	if reads.Load() != 0 {
		t.Fatal("validation read containers")
	}
	otherEngine := httptest.NewServer(engine("secondary"))
	defer otherEngine.Close()
	other, err := nodes.Create(context.Background(), node.Input{Name: "Secondary overview", ConnectionType: node.ConnectionTCP, Endpoint: strings.Replace(otherEngine.URL, "http://", "tcp://", 1), TLSMode: node.TLSDisabled, PlaintextConfirmation: "127.0.0.1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ nodeID, name string }{{"local", "primary"}, {other.ID, "secondary"}} {
		out := request(context.Background(), "/api/v1/nodes/"+c.nodeID+"/containers/app/overview", true)
		if out.Code != 200 || out.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("overview failed or was cacheable")
		}
		var result struct {
			Data domain.Overview `json:"data"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Data.ID != "app" || len(result.Data.Networks) != 1 || result.Data.Networks[0].Name != c.name {
			t.Fatal("overview leaked across nodes")
		}
		for _, secret := range []string{"http-private", "argument-private", "label-private"} {
			if strings.Contains(out.Body.String(), secret) {
				t.Fatal("HTTP overview exposed a secret")
			}
		}
	}
	if reads.Load() != 2 {
		t.Fatal("overview did extra inspections")
	}
	for _, c := range []struct {
		id   string
		code int
	}{{"gone", 404}, {"broken", 503}} {
		out := request(context.Background(), "/api/v1/nodes/local/containers/"+c.id+"/overview", true)
		if out.Code != c.code || strings.Contains(out.Body.String(), "engine-private") {
			t.Fatal("unsafe/incorrect error response")
		}
	}
}

func TestContainerOverviewHTTPParentCancellation(t *testing.T) {
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
	go func() { done <- request(ctx, "/api/v1/nodes/local/containers/app/overview", true) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("inspection did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream inspection not canceled")
	}
	select {
	case out := <-done:
		if out.Code != 503 {
			t.Fatal("canceled inspection did not fail")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP read held canceled request")
	}
}
