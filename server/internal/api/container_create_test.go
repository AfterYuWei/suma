package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

func TestContainerCreationHTTPNodeIsolationAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, store, "unix:///unused.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	var creates [2]atomic.Int32
	engine := func(index int) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := projectDockerVersion.ReplaceAllString(r.URL.Path, "")
			switch {
			case path == "/_ping":
				w.Header().Set("Api-Version", "1.44")
				_, _ = w.Write([]byte("OK"))
			case path == "/info":
				writeProjectDockerJSON(w, map[string]any{"ID": []string{"primary-engine", "secondary-engine"}[index], "Name": "fixture", "OSType": "linux"})
			case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
				writeProjectDockerJSON(w, map[string]any{"Id": "sha256:" + strings.Repeat("ab", 32)})
			case path == "/containers/create":
				creates[index].Add(1)
				var config map[string]any
				if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
					t.Error(err)
				}
				if config["Image"] != "nginx:alpine" {
					t.Error("creation lost image")
				}
				w.WriteHeader(201)
				writeProjectDockerJSON(w, map[string]any{"Id": strings.Repeat([]string{"a", "b"}[index], 64), "Warnings": []string{}})
			case strings.HasSuffix(path, "/start"):
				w.WriteHeader(204)
			default:
				t.Errorf("unexpected Engine request: %s %s", r.Method, path)
				w.WriteHeader(404)
			}
		}))
		t.Cleanup(server.Close)
		return server
	}
	local, remote := engine(0), engine(1)
	if err := db.Model(&database.Node{}).Where("id = ?", "local").Updates(map[string]any{"connection_type": "tcp", "endpoint": strings.Replace(local.URL, "http://", "tcp://", 1), "tls_mode": "disabled"}).Error; err != nil {
		t.Fatal(err)
	}
	other, err := nodes.Create(context.Background(), node.Input{Name: "Other", ConnectionType: "tcp", Endpoint: strings.Replace(remote.URL, "http://", "tcp://", 1), TLSMode: "disabled", PlaintextConfirmation: "127.0.0.1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	disabled := node.View{ID: "disabled"}
	if err := db.Create(&database.Node{ID: disabled.ID, Name: "Disabled", ConnectionType: "unix", Endpoint: "unix:///unused-disabled.sock", TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(db, time.Hour)
	if _, err := authentication.Initialize(context.Background(), "admin", "admin@example.test", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authentication.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	registries := credential.NewRegistryService(db, store)
	registry, err := registries.Create(context.Background(), credential.RegistryInput{Name: "Private", ServerAddress: "ghcr.io", AuthType: "basic", Username: "test", Secret: "registry-private", AuthorizedNodeIDs: []string{"local"}})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Nodes: nodes, Auth: authentication, Tasks: tasks, Audit: audit.NewService(db), RegistryCredentials: registries})
	request := func(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://example.com")
		if authenticated {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	valid := `{"name":"new-app","image":"nginx:alpine","environment":[{"key":"PASSWORD","value":"http-creation-private"}]}`
	for _, scenario := range []struct {
		id, body      string
		authenticated bool
		code          int
	}{
		{"local", valid, false, 401}, {"missing", valid, true, 404}, {disabled.ID, valid, true, 503},
		{"local", `{"name":"../bad","image":"nginx:alpine"}`, true, 400},
		{"local", `{"name":"app","image":"nginx:alpine","mounts":[{"type":"bind","source":"./data","target":"/data"}]}`, true, 400},
		{"local", `{"name":"app","image":"nginx:alpine","mounts":[{"type":"bind","source":"/var/run/docker.sock","target":"/var/run/docker.sock","read_only":true}]}`, true, 400},
		{"local", `{"name":"app","image":"nginx:alpine","ports":[{"container_port":65536}]}`, true, 400},
		{"local", `{"name":"app","image":"nginx:alpine","user":"` + strings.Repeat("x", 256<<10) + `"}`, true, 400},
	} {
		response := request("POST", "/api/v1/nodes/"+scenario.id+"/containers", scenario.body, scenario.authenticated)
		if response.Code != scenario.code {
			t.Fatalf("rejection: %d wanted %d: %s", response.Code, scenario.code, response.Body)
		}
	}
	if creates[0].Load()+creates[1].Load() != 0 {
		t.Fatal("rejected requests created containers")
	}
	for _, id := range []string{"local", other.ID} {
		body := `{"name":"app","image":"nginx:alpine","credential_id":` + jsonNumber(registry.ID) + `}`
		response := request("POST", "/api/v1/nodes/"+id+"/containers", body, true)
		want := 400
		if id == other.ID {
			want = 403
		}
		if response.Code != want || strings.Contains(response.Body.String(), "registry-private") {
			t.Fatal("registry authorization or domain boundary failed", response.Code)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, id := range []string{"local", other.ID} {
		response := request("POST", "/api/v1/nodes/"+id+"/containers", valid, true)
		if response.Code != 202 || strings.Contains(response.Body.String(), "http-creation-private") {
			t.Fatal("create response", response.Code, response.Body)
		}
		var result struct {
			Data database.Task `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Data.NodeID != id || result.Data.Type != "container.create" {
			t.Fatal("task lost node")
		}
		if err := tasks.Wait(ctx, result.Data.ID); err != nil {
			t.Fatal(err)
		}
		row, _ := tasks.GetForNode(ctx, id, result.Data.ID)
		if row.Status != task.StatusSuccess {
			t.Fatal("creation failed", row.Message)
		}
		wrong := "local"
		if id == "local" {
			wrong = other.ID
		}
		if response := request("GET", "/api/v1/nodes/"+wrong+"/tasks/"+row.ID, "", true); response.Code != 404 {
			t.Fatal("cross-node task accessible")
		}
	}
	if creates[0].Load() != 1 || creates[1].Load() != 1 {
		t.Fatal("container creation reached wrong Engine")
	}
}

func jsonNumber(value uint) string { body, _ := json.Marshal(value); return string(body) }
