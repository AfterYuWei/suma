package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/system"
	composedomain "github.com/suma/suma/server/internal/compose"
	domain "github.com/suma/suma/server/internal/container"
	networkdomain "github.com/suma/suma/server/internal/network"
	volumedomain "github.com/suma/suma/server/internal/volume"
)

// The adapter constructor accepts any host URL, so tests point it at an
// httptest.Server that mimics a tiny subset of the Engine API. No real
// Docker daemon or Unix socket is involved.
var apiVersionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

type stubRequest struct {
	Method string
	Path   string
	Query  url.Values
}

type dockerStub struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []stubRequest
	handlers map[string]http.HandlerFunc // keyed by cleaned path without version prefix
}

func newDockerStub(t *testing.T, handlers map[string]http.HandlerFunc) *dockerStub {
	t.Helper()
	stub := &dockerStub{handlers: map[string]http.HandlerFunc{
		"/_ping": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Api-Version", "1.44")
			w.Header().Set("Ostype", "linux")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "OK")
		},
	}}
	for path, handler := range handlers {
		if strings.HasSuffix(path, "/_ping") {
			continue
		}
		stub.handlers[path] = handler
	}
	stub.server = httptest.NewServer(stub)
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *dockerStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cleaned := apiVersionPrefix.ReplaceAllString(r.URL.Path, "")
	s.mu.Lock()
	s.requests = append(s.requests, stubRequest{Method: r.Method, Path: cleaned, Query: r.URL.Query()})
	s.mu.Unlock()
	handler, ok := s.handlers[cleaned]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("unexpected %s %s", r.Method, cleaned)})
		return
	}
	handler(w, r)
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(body)
}

func (s *dockerStub) find(t *testing.T, predicate func(stubRequest) bool) []stubRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var found []stubRequest
	for _, request := range s.requests {
		if predicate(request) {
			found = append(found, request)
		}
	}
	return found
}

func requireRequested(t *testing.T, s *dockerStub, path string) {
	t.Helper()
	requests := s.find(t, func(request stubRequest) bool { return request.Path == path })
	if len(requests) == 0 {
		t.Fatalf("expected %q to be requested, got %#v", path, s.find(t, func(stubRequest) bool { return true }))
	}
}

func newAdapter(t *testing.T, stub *dockerStub) *Adapter {
	t.Helper()
	adapter, err := New(stub.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adapter.Close() })
	return adapter
}

func TestAdapterPing(t *testing.T) {
	ctx := context.Background()
	stub := newDockerStub(t, nil)
	adapter := newAdapter(t, stub)

	if err := adapter.Ping(ctx); err != nil {
		t.Fatalf("ping against fake engine failed: %v", err)
	}
	pings := stub.find(t, func(request stubRequest) bool {
		return request.Path == "/_ping" && (request.Method == http.MethodHead || request.Method == http.MethodGet)
	})
	if len(pings) == 0 {
		t.Fatalf("expected a /_ping request, got %#v", stub.requests)
	}

	// Error propagation when the endpoint cannot be reached at all.
	unreachable := httptest.NewServer(http.NotFoundHandler())
	url := unreachable.URL
	unreachable.Close()
	broken, err := New(url)
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	err = broken.Ping(ctx)
	if err == nil || !strings.Contains(err.Error(), "ping Docker") {
		t.Fatalf("expected wrapped ping failure, got %v", err)
	}
}

func TestAdapterInfoMapping(t *testing.T) {
	ctx := context.Background()
	engine := system.Info{
		ID:                "engine-id-1",
		Name:              "node-a",
		ServerVersion:     "27.3.1",
		OperatingSystem:   "Debian GNU/Linux 12",
		OSType:            "linux",
		Architecture:      "x86_64",
		KernelVersion:     "6.8.0",
		Containers:        5,
		ContainersRunning: 3,
		ContainersStopped: 2,
		Images:            11,
		NCPU:              8,
		MemTotal:          16 << 30,
	}
	stub := newDockerStub(t, map[string]http.HandlerFunc{
		"/info": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, engine)
		},
	})
	adapter := newAdapter(t, stub)

	info, err := adapter.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireRequested(t, stub, "/info")
	want := Info{ID: "engine-id-1", Name: "node-a", ServerVersion: "27.3.1", OperatingSystem: "Debian GNU/Linux 12", OSType: "linux", Architecture: "x86_64", KernelVersion: "6.8.0", Containers: 5, Running: 3, Stopped: 2, Images: 11, CPUs: 8, MemoryBytes: 16 << 30}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("unexpected mapped info:\n got %#v\nwant %#v", info, want)
	}
}

func TestAdapterListContainers(t *testing.T) {
	ctx := context.Background()
	created := int64(1700000000)
	fullID := strings.Repeat("ab", 32)
	rows := []dockercontainer.Summary{
		{
			ID:      fullID,
			Names:   []string{"/web"},
			Image:   "nginx:1.27",
			Command: "nginx -g 'daemon off;'",
			Created: created,
			State:   "running",
			Status:  "Up 2 hours",
			Ports: []dockercontainer.Port{
				{IP: "0.0.0.0", PrivatePort: 80, PublicPort: 8080, Type: "tcp"},
				{PrivatePort: 443, Type: "tcp"},
			},
			Labels: map[string]string{"app": "web"},
		},
		{
			ID:      strings.Repeat("cd", 32),
			Image:   "redis:7",
			Command: "redis-server",
			Created: created,
			State:   "exited",
			Status:  "Exited (0)",
		},
	}
	stub := newDockerStub(t, map[string]http.HandlerFunc{
		"/containers/json": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, rows)
		},
	})
	adapter := newAdapter(t, stub)

	list, err := adapter.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireRequested(t, stub, "/containers/json")
	for _, request := range stub.find(t, func(request stubRequest) bool { return request.Path == "/containers/json" }) {
		if request.Query.Get("all") != "1" {
			t.Fatalf("expected all=1 query parameter on container list, got %#v", request.Query)
		}
		if request.Query.Get("filters") != "" {
			t.Fatalf("plain list must not carry filter parameters, got %s", request.Query.Get("filters"))
		}
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(list))
	}
	first, second := list[0], list[1]
	if first.ID != fullID || first.Name != "web" || first.Image != "nginx:1.27" || first.Command != "nginx -g 'daemon off;'" ||
		first.State != "running" || first.Status != "Up 2 hours" || !reflect.DeepEqual(first.Labels, map[string]string{"app": "web"}) {
		t.Fatalf("unexpected first summary: %#v", first)
	}
	if !first.Created.Equal(time.Unix(created, 0)) {
		t.Fatalf("created time not derived from unix seconds: %v", first.Created)
	}
	if len(first.Ports) != 2 {
		t.Fatalf("expected 2 ports, got %#v", first.Ports)
	}
	if first.Ports[0] != (domain.Port{PrivatePort: 80, PublicPort: 8080, Type: "tcp", IP: "0.0.0.0"}) {
		t.Fatalf("unexpected published port mapping: %#v", first.Ports[0])
	}
	if first.Ports[1] != (domain.Port{PrivatePort: 443, Type: "tcp"}) {
		t.Fatalf("unexpected exposed port: %#v", first.Ports[1])
	}
	if second.Name != second.ID[:12] || second.State != "exited" || second.Image != "redis:7" {
		t.Fatalf("unnamed container must fall back to short id: %#v", second)
	}
	if second.Ports != nil && len(second.Ports) != 0 {
		t.Fatalf("unexpected ports on unnamed container: %#v", second.Ports)
	}
}

func TestAdapterInspectsWholeComposeProject(t *testing.T) {
	ctx := context.Background()
	containerID := strings.Repeat("ab", 32)
	imageID := "sha256:" + strings.Repeat("cd", 32)
	labels := map[string]string{
		composedomain.ProjectLabel: "shop", composedomain.ServiceLabel: "web",
		composedomain.ContainerNumberLabel: "2", composedomain.ConfigHashLabel: "config-v1",
	}
	stub := newDockerStub(t, map[string]http.HandlerFunc{
		"/containers/json": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, []dockercontainer.Summary{{ID: containerID, Labels: labels}})
		},
		"/containers/" + containerID + "/json": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"Id": containerID, "Name": "/shop-web-2", "Image": imageID, "Created": "2026-08-27T00:00:00Z",
				"Config":          map[string]any{"Image": "example/web:v1", "Env": []string{"PATH=/usr/bin", "MODE=prod"}, "Labels": labels, "Cmd": []string{"serve"}},
				"HostConfig":      map[string]any{"RestartPolicy": map[string]any{"Name": "unless-stopped"}, "PortBindings": map[string]any{"8080/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "18080"}}}},
				"State":           map[string]any{"Status": "running", "Running": true},
				"NetworkSettings": map[string]any{"Networks": map[string]any{"shop_default": map[string]any{"Aliases": []string{"web"}}}},
				"Mounts":          []map[string]any{{"Type": "volume", "Name": "shop_data", "Source": "/var/lib/docker/volumes/shop_data/_data", "Destination": "/data", "RW": true}},
			})
		},
		"/images/" + imageID + "/json": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"Id": imageID, "Config": map[string]any{"Env": []string{"PATH=/usr/bin"}}})
		},
		"/networks/shop_default": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"Name": "shop_default", "Id": "network-id", "Driver": "bridge", "Labels": map[string]string{composedomain.ProjectLabel: "shop"}})
		},
		"/volumes/shop_data": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"Name": "shop_data", "Driver": "local", "Scope": "local", "Labels": map[string]string{composedomain.ProjectLabel: "shop"}})
		},
	})
	adapter := newAdapter(t, stub)
	value, err := adapter.InspectComposeProject(ctx, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Containers) != 1 || value.Containers[0].Service != "web" || value.Containers[0].ContainerNumber != 2 || !value.Containers[0].ImageInspectOK {
		t.Fatalf("snapshot = %#v", value)
	}
	if len(value.Containers[0].Config.Ports) != 1 || value.Containers[0].Config.Ports[0].Published != 18080 {
		t.Fatalf("ports = %#v", value.Containers[0].Config.Ports)
	}
	if len(value.Networks) != 1 || len(value.Volumes) != 1 {
		t.Fatalf("resources = %#v / %#v", value.Networks, value.Volumes)
	}
	requests := stub.find(t, func(request stubRequest) bool { return request.Path == "/containers/json" })
	if len(requests) != 1 || !strings.Contains(requests[0].Query.Get("filters"), composedomain.ProjectLabel) {
		t.Fatalf("container filters = %#v", requests)
	}
}

func TestAdapterListNetworksMapping(t *testing.T) {
	ctx := context.Background()
	networks := []dockernetwork.Inspect{
		{
			Name:     "dockport-web",
			ID:       strings.Repeat("12", 32),
			Scope:    "local",
			Driver:   "bridge",
			Internal: true,
			Labels:   map[string]string{"dockport.project": "web"},
			IPAM: dockernetwork.IPAM{Driver: "default", Config: []dockernetwork.IPAMConfig{
				{Subnet: "172.28.0.0/16", Gateway: "172.28.0.1"},
			}},
			Containers: map[string]dockernetwork.EndpointResource{
				strings.Repeat("ef", 16): {Name: "api", IPv4Address: "172.28.0.2/16"},
				strings.Repeat("ab", 16): {Name: "db", IPv4Address: "172.28.0.3/16"},
			},
		},
		{
			Name:       "host-net",
			ID:         strings.Repeat("34", 32),
			Scope:      "local",
			Driver:     "bridge",
			EnableIPv6: true,
		},
	}
	stub := newDockerStub(t, map[string]http.HandlerFunc{
		"/networks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, networks)
		},
	})
	adapter := newAdapter(t, stub)

	list, err := adapter.ListNetworks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireRequested(t, stub, "/networks")
	if len(list) != 2 {
		t.Fatalf("expected 2 networks, got %d", len(list))
	}
	first, second := list[0], list[1]
	wantFirst := networkdomain.Resource{
		ID: strings.Repeat("12", 32), Name: "dockport-web", Driver: "bridge", Scope: "local",
		Internal: true, Containers: 2, Labels: map[string]string{"dockport.project": "web"},
		IPAM: []networkdomain.IPAM{{Subnet: "172.28.0.0/16", Gateway: "172.28.0.1"}},
	}
	if !reflect.DeepEqual(first, wantFirst) {
		t.Fatalf("unexpected mapped network:\n got %#v\nwant %#v", first, wantFirst)
	}
	if second.ID != strings.Repeat("34", 32) || !second.IPv6 || second.Internal {
		t.Fatalf("unexpected secondary network mapping: %#v", second)
	}
	if len(second.IPAM) != 0 || second.Containers != 0 {
		t.Fatalf("empty ipam/container mapping expected, got %#v", second)
	}
}

func TestAdapterRemoveVolumeUsageGuard(t *testing.T) {
	ctx := context.Background()

	t.Run("in use volume is rejected before delete", func(t *testing.T) {
		usingID := strings.Repeat("77", 32)
		stub := newDockerStub(t, map[string]http.HandlerFunc{
			"/containers/json": func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, []dockercontainer.Summary{{ID: usingID, Names: []string{"/api"}}})
			},
			"/volumes/cache-data": func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]string{})
			},
		})
		adapter := newAdapter(t, stub)

		err := adapter.RemoveVolume(ctx, "cache-data")
		if !errors.Is(err, volumedomain.ErrInUse) {
			t.Fatalf("expected ErrInUse, got %v", err)
		}
		usageRequests := stub.find(t, func(request stubRequest) bool { return request.Path == "/containers/json" })
		if len(usageRequests) == 0 {
			t.Fatal("volume usage lookup never listed containers")
		}
		var sawVolumeFilter bool
		for _, request := range usageRequests {
			filters := make(map[string]map[string]bool)
			if raw := request.Query.Get("filters"); raw != "" {
				if json.Unmarshal([]byte(raw), &filters) == nil && filters["volume"]["cache-data"] {
					sawVolumeFilter = true
				}
			}
		}
		if !sawVolumeFilter {
			t.Fatalf("expected volume=cache-data filter on usage lookup, got %#v", usageRequests[len(usageRequests)-1].Query)
		}
		deletes := stub.find(t, func(request stubRequest) bool {
			return request.Method == http.MethodDelete && request.Path == "/volumes/cache-data"
		})
		if len(deletes) != 0 {
			t.Fatal("volume delete must not run while the volume is in use")
		}
	})

	t.Run("unused volume is deleted", func(t *testing.T) {
		stub := newDockerStub(t, map[string]http.HandlerFunc{
			"/containers/json": func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, []dockercontainer.Summary{})
			},
			"/volumes/webdata": func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]string{})
			},
		})
		adapter := newAdapter(t, stub)

		if err := adapter.RemoveVolume(ctx, "webdata"); err != nil {
			t.Fatalf("remove unused volume: %v", err)
		}
		requireRequested(t, stub, "/volumes/webdata")
		deletes := stub.find(t, func(request stubRequest) bool {
			return request.Method == http.MethodDelete && request.Path == "/volumes/webdata"
		})
		if len(deletes) != 1 {
			t.Fatalf("expected exactly one DELETE for the unused volume, got %#v", deletes)
		}
	})
}
