//go:build dockersmoke

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/suma/suma/server/internal/node"
)

// This smoke only reads the real Engine; accounts/nodes are confined to the
// disposable PostgreSQL test schema. No containers or volumes are modified.
func TestRealDockerNodeOverviewReads(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1 to read the real Docker Engine")
	}
	_, nodes, request := overviewHTTPFixture(t, http.NotFoundHandler())
	ctx := context.Background()
	if _, err := nodes.Update(ctx, "local", node.Input{Name: "Smoke", ConnectionType: node.ConnectionUnix, Endpoint: "unix:///var/run/docker.sock", TLSMode: node.TLSDisabled, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"info", "metrics?size=small", "metrics?size=medium", "metrics?size=large", "resources", "disk"} {
		response := request(ctx, "/api/v1/nodes/local/overview/"+suffix, true)
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", suffix, response.Code, response.Body.String())
		}
		var result struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Data) == 0 {
			t.Fatalf("%s returned invalid data", suffix)
		}
		if suffix == "metrics?size=small" && len(result.Data) != 3 {
			t.Fatal("small profile returned hidden fields")
		}
		t.Logf("read %s successfully", suffix)
	}
}
