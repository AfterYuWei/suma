package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/compose"
)

func TestInspectComposeNamespaceIncludesUnusedResourcesAndFiltersEveryRequest(t *testing.T) {
	for _, failedPath := range []string{"", "/networks", "/volumes"} {
		t.Run("error="+failedPath, func(t *testing.T) {
			handlers := map[string]http.HandlerFunc{}
			for path, payload := range map[string]any{
				"/containers/json": []any{},
				"/networks":        []any{map[string]any{"Id": "unused-network", "Name": "eizhu_admin_default"}},
				"/volumes":         map[string]any{"Volumes": []any{map[string]any{"Name": "eizhu_admin_data"}}},
			} {
				handlers[path] = func(w http.ResponseWriter, r *http.Request) {
					var filter struct {
						Label map[string]bool `json:"label"`
					}
					if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filter); err != nil || len(filter.Label) != 1 || !filter.Label[compose.ProjectLabel+"=eizhu_admin"] {
						t.Errorf("unexpected namespace filter: %s", r.URL.RawQuery)
					}
					if path == "/containers/json" && r.URL.Query().Get("all") != "1" {
						t.Error("stopped containers were excluded")
					}
					if path == failedPath {
						writeJSON(w, 500, map[string]string{"message": "namespace inventory failed"})
						return
					}
					writeJSON(w, 200, payload)
				}
			}
			stub := newDockerStub(t, handlers)
			adapter, err := New(stub.server.URL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = adapter.Close() })
			result, err := adapter.InspectComposeNamespace(context.Background(), "eizhu_admin")
			if failedPath != "" {
				if err == nil || !strings.Contains(err.Error(), "namespace inventory failed") {
					t.Fatalf("inventory failure was ignored: %v", err)
				}
				return
			}
			if err != nil || len(result.Containers) != 0 || len(result.Networks) != 1 || len(result.Volumes) != 1 {
				t.Fatalf("unused resources not returned: %#v, %v", result, err)
			}
		})
	}
}
