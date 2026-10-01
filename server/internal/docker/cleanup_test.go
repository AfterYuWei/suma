package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/cleanup"
)

func TestCleanupCacheUsesNegotiatedVersionAndNeverAll(t *testing.T) {
	for _, version := range []string{"1.39", "1.44", "1.51", "1.38", "1.30"} {
		t.Run(version, func(t *testing.T) {
			stub := newDockerStub(t, map[string]http.HandlerFunc{"/build/prune": func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, map[string]any{"CachesDeleted": []string{"old"}, "SpaceReclaimed": 123})
			}})
			stub.handlers["/_ping"] = func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Api-Version", version)
				w.WriteHeader(200)
			}
			adapter := newAdapter(t, stub)
			report, err := adapter.CleanupPruneCache(context.Background(), cleanup.CacheOptions{RetentionDays: 7, ReservedBytes: 10 << 30})
			requests := stub.find(t, func(r stubRequest) bool { return r.Path == "/build/prune" })
			if version == "1.30" || version == "1.38" {
				if err == nil || len(requests) > 0 {
					t.Fatal("unsupported API pruned")
				}
				return
			}
			if err != nil || report.ReclaimedBytes != 123 {
				t.Fatalf("%+v %v", report, err)
			}
			if len(requests) != 1 {
				t.Fatal(requests)
			}
			query := requests[0].Query
			if query.Get("all") != "" || query.Get("force") != "" {
				t.Fatal("aggressive cache prune")
			}
			var filters map[string]map[string]bool
			if err = json.Unmarshal([]byte(query.Get("filters")), &filters); err != nil || !filters["until"]["168h"] {
				t.Fatal(query)
			}
			expected := "keep-storage"
			other := "reserved-space"
			if version == "1.51" {
				expected, other = other, expected
			}
			if query.Get(expected) != "10737418240" || query.Get(other) != "" {
				t.Fatalf("wrong version compatibility: %v", query)
			}
		})
	}
}
func TestCleanupInventoryCountsStoppedReferencesAndUnknownSizes(t *testing.T) {
	old := time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)
	stub := newDockerStub(t, map[string]http.HandlerFunc{
		"/system/df": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"LayersSize": 100, "Containers": []any{map[string]any{"Id": "stopped", "SizeRw": -1}}, "Volumes": []any{map[string]any{"Name": "data", "UsageData": map[string]any{"Size": -1}}}})
		},
		"/containers/json": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("all") != "1" {
				t.Error("stopped containers excluded")
			}
			writeJSON(w, 200, []any{map[string]any{"Id": "stopped", "ImageID": "sha256:image", "Mounts": []any{map[string]any{"Type": "volume", "Name": "data"}}, "NetworkSettings": map[string]any{"Networks": map[string]any{"net": map[string]any{"NetworkID": "network"}}}}})
		},
		"/containers/stopped/json": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"Id": "stopped", "Name": "/worker", "Created": old, "Config": map[string]any{}, "State": map[string]any{"Status": "exited", "FinishedAt": time.Now().Add(-time.Hour).Format(time.RFC3339Nano)}})
		},
		"/images/json": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, []any{map[string]any{"Id": "sha256:image", "Created": time.Now().Add(-30 * 24 * time.Hour).Unix(), "Size": 200, "Containers": -1, "RepoTags": []string{"app:old"}}})
		},
		"/networks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, []any{map[string]any{"Id": "network", "Name": "net", "Created": old}})
		},
		"/volumes": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"Volumes": []any{map[string]any{"Name": "data", "CreatedAt": old}}})
		},
	})
	adapter := newAdapter(t, stub)
	inv, err := adapter.CleanupInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inv.LayersBytes == nil || *inv.LayersBytes != 100 {
		t.Fatal("image sizes were summed")
	}
	if inv.Usage.ContainerBytes != nil || inv.Usage.VolumeBytes != nil {
		t.Fatal("unknown Docker usage reported as zero")
	}
	for _, r := range inv.Resources {
		switch r.Kind {
		case cleanup.Image, cleanup.Network, cleanup.Volume:
			if !r.InUse {
				t.Errorf("%s stopped reference missed", r.Kind)
			}
		case cleanup.Container:
			if r.SizeBytes != nil || r.FinishedAt.Before(time.Now().Add(-2*time.Hour)) {
				t.Fatal("unknown size or actual stop time lost")
			}
		}
	}
}
func TestCleanupRemoveNeverForcesOrPrunesExtraImages(t *testing.T) {
	for _, kind := range []cleanup.Kind{cleanup.Container, cleanup.Image, cleanup.Network} {
		t.Run(string(kind), func(t *testing.T) {
			handlers := map[string]http.HandlerFunc{
				"/containers/json": func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, []any{}) },
				"/containers/item/json": func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, 200, map[string]any{"Id": "item", "Name": "/worker", "Config": map[string]any{}, "State": map[string]any{"Status": "exited"}})
				},
				"/images/item/json": func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, 200, map[string]any{"Id": "item", "Config": map[string]any{}})
				},
				"/networks/item": func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						writeJSON(w, 200, map[string]any{"Id": "item", "Name": "scratch"})
					} else {
						w.WriteHeader(204)
					}
				},
				"/containers/item": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) },
				"/images/item":     func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, []any{}) },
			}
			stub := newDockerStub(t, handlers)
			adapter := newAdapter(t, stub)
			if err := adapter.CleanupRemove(context.Background(), kind, "item"); err != nil {
				t.Fatal(err)
			}
			deletions := stub.find(t, func(r stubRequest) bool { return r.Method == "DELETE" })
			if len(deletions) != 1 {
				t.Fatal(deletions)
			}
			query := deletions[0].Query
			if query.Get("force") == "1" || query.Get("v") == "1" || query.Get("noprune") == "0" {
				t.Fatal("unsafe delete options", query)
			}
			if kind == cleanup.Image && query.Get("noprune") != "1" {
				t.Fatal("extra parent images could be deleted", query)
			}
		})
	}
}
func TestCleanupProtectsStoppedComposeAndDefaultNetwork(t *testing.T) {
	for _, kind := range []cleanup.Kind{cleanup.Container, cleanup.Network} {
		t.Run(string(kind), func(t *testing.T) {
			stub := newDockerStub(t, map[string]http.HandlerFunc{
				"/containers/json": func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, []any{}) },
				"/containers/item/json": func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, 200, map[string]any{"Id": "item", "Name": "/worker", "Config": map[string]any{"Labels": map[string]string{"com.docker.compose.project": "external"}}, "State": map[string]any{"Status": "exited"}})
				},
				"/networks/item": func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, 200, map[string]any{"Id": "item", "Name": "bridge"})
				},
			})
			adapter := newAdapter(t, stub)
			if err := adapter.CleanupRemove(context.Background(), kind, "item"); err == nil {
				t.Fatal("protected resource removed")
			}
			if len(stub.find(t, func(r stubRequest) bool { return r.Method == "DELETE" })) > 0 {
				t.Fatal("delete request sent")
			}
		})
	}
}
func TestCleanupVolumeCannotUseAutomaticExecutor(t *testing.T) {
	stub := newDockerStub(t, nil)
	adapter := newAdapter(t, stub)
	// No automatic volume DELETE can be sent even if the caller requests it.
	_ = adapter.CleanupRemove(context.Background(), cleanup.Volume, "data")
	for _, request := range stub.requests {
		if request.Method == "DELETE" {
			t.Fatal(request)
		}
	}
}
func TestCleanupCanonicalImageReferences(t *testing.T) {
	aliases := imageAliases("nginx:alpine")
	if !strings.Contains(fmt.Sprint(aliases), "docker.io/library/nginx:alpine") {
		t.Fatal(aliases)
	}
}

func TestCleanupProtectsControlPlaneAgentAndBuildxContainers(t *testing.T) {
	for _, image := range []string{"suma:dev", "suma-agent:latest", "ghcr.io/example/suma@sha256:123", "moby/buildkit:latest"} {
		t.Run(image, func(t *testing.T) {
			stub := newDockerStub(t, map[string]http.HandlerFunc{"/containers/item/json": func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, map[string]any{"Id": "item", "Name": "/custom-name", "Config": map[string]any{"Image": image}, "State": map[string]any{"Status": "exited"}})
			}})
			if err := newAdapter(t, stub).CleanupRemove(context.Background(), cleanup.Container, "item"); err == nil {
				t.Fatal("system container removed")
			}
			if len(stub.find(t, func(r stubRequest) bool { return r.Method == "DELETE" })) != 0 {
				t.Fatal("protected delete sent")
			}
		})
	}
}
