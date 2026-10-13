package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/node"
)

func TestContainerListNetworksIsolationAndRemovedOverview(t *testing.T) {
	var reads, extraReads atomic.Int32
	engine := func(id, ip string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			path := projectDockerVersion.ReplaceAllString(r.URL.Path, "")
			switch path {
			case "/info":
				writeProjectDockerJSON(w, map[string]any{"ID": id, "Name": id, "ServerVersion": "28.5"})
			case "/containers/json":
				reads.Add(1)
				if r.URL.Query().Get("all") != "1" {
					t.Error("list excluded stopped containers")
				}
				writeProjectDockerJSON(w, []map[string]any{{"Id": id, "State": "running", "NetworkSettings": map[string]any{"Networks": map[string]any{
					"private":     map[string]any{"GlobalIPv6Address": "fd00::2"},
					"app_default": map[string]any{"IPAddress": ip},
				}}}})
			default:
				if path != "/_ping" {
					extraReads.Add(1)
				}
				overviewEngineJSON(w, r, path)
			}
		}
	}
	_, nodes, request := overviewHTTPFixture(t, engine("primary", "172.20.0.2"))
	ctx := context.Background()
	for _, check := range []struct {
		path string
		auth bool
		code int
	}{
		{"/api/v1/nodes/local/containers", false, 401},
		{"/api/v1/nodes/missing/containers", true, 404},
		{"/api/v1/nodes/disabled/containers", true, 503},
		{"/api/v1/nodes/local/containers/primary/overview", true, 404},
	} {
		if response := request(ctx, check.path, check.auth); response.Code != check.code {
			t.Fatalf("%s = %d", check.path, response.Code)
		}
	}
	if reads.Load() != 0 || extraReads.Load() != 0 {
		t.Fatal("rejected list or removed overview accessed Docker")
	}
	second := httptest.NewServer(engine("secondary", "172.21.0.2"))
	defer second.Close()
	other, err := nodes.Create(ctx, node.Input{Name: "Secondary", ConnectionType: node.ConnectionTCP, Endpoint: strings.Replace(second.URL, "http://", "tcp://", 1), TLSMode: node.TLSDisabled, PlaintextConfirmation: "127.0.0.1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ nodeID, engineID, ip string }{{"local", "primary", "172.20.0.2"}, {other.ID, "secondary", "172.21.0.2"}} {
		response := request(ctx, "/api/v1/nodes/"+check.nodeID+"/containers", true)
		if response.Code != 200 {
			t.Fatalf("list = %d", response.Code)
		}
		var result struct {
			Data []containerdomain.Summary `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Data) != 1 || result.Data[0].ID != check.engineID || !reflect.DeepEqual(result.Data[0].Networks, []containerdomain.NetworkSummary{{Name: "app_default", IPAddress: check.ip}, {Name: "private", IPv6Address: "fd00::2"}}) {
			t.Fatalf("node network summary leaked or lost addresses: %#v", result.Data)
		}
	}
	if reads.Load() != 2 || extraReads.Load() != 0 {
		t.Fatal("network summary required additional Engine reads")
	}
}
