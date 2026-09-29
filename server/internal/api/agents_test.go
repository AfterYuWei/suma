package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/suma/suma/server/internal/agenthub"
	"github.com/suma/suma/server/internal/agentwire"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
)

func TestAgentEnrollmentAndDockerRuntimeOverWSS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	dockerSocket := filepath.Join(root, "docker.sock")
	listener, err := net.Listen("unix", dockerSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	versioned := regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)
	engine := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch versioned.ReplaceAllString(r.URL.Path, "") {
		case "/_ping":
			w.Header().Set("Api-Version", "1.44")
			_, _ = fmt.Fprint(w, "OK")
		case "/info":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ID": "agent-engine", "Name": "agent-test", "ServerVersion": "29.0", "OSType": "linux"})
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = engine.Serve(listener) }()
	defer engine.Close()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, store, "unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	hub, err := agenthub.New()
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	nodes.SetAgentHub(hub)
	adminAuth := auth.NewService(db, time.Hour)
	router := NewRouter(Dependencies{Nodes: nodes, Agents: hub, Auth: adminAuth, Audit: audit.NewService(db), AgentPublicURL: "https://suma.example"})
	server := httptest.NewServer(router)
	defer server.Close()
	unauthorized := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/agent-enrollments", strings.NewReader(`{"name":"edge"}`))
	unauthorizedRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(unauthorized, unauthorizedRequest)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("enrollment creation bypassed session: %d", unauthorized.Code)
	}
	if _, err := adminAuth.Initialize(context.Background(), "admin", "admin@example.test", "", "AgentAdmin123!"); err != nil {
		t.Fatal(err)
	}
	adminToken, _, err := adminAuth.Login(context.Background(), "admin", "AgentAdmin123!", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	adminRequest := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.AddCookie(&http.Cookie{Name: sessionCookie, Value: adminToken})
		request.Header.Set("Origin", "http://"+request.Host)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	created := adminRequest(http.MethodPost, "/api/v1/agent-enrollments", `{"name":"API-paired node"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("admin pairing: %d %s", created.Code, created.Body.String())
	}
	var adminEnrollment struct {
		Data struct {
			NodeID string `json:"node_id"`
			Token  string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &adminEnrollment); err != nil || adminEnrollment.Data.NodeID == "" || len(adminEnrollment.Data.Token) != 64 {
		t.Fatalf("invalid pairing response: %s, %v", created.Body.String(), err)
	}
	status := adminRequest(http.MethodGet, "/api/v1/agent-enrollments/"+adminEnrollment.Data.NodeID, "")
	if status.Code != http.StatusOK || strings.Contains(status.Body.String(), adminEnrollment.Data.Token) {
		t.Fatalf("pairing status exposed one-time token: %d %s", status.Code, status.Body.String())
	}
	reissued := adminRequest(http.MethodPost, "/api/v1/agent-enrollments/"+adminEnrollment.Data.NodeID+"/reissue", "")
	if reissued.Code != http.StatusOK || !strings.Contains(reissued.Body.String(), `"token":`) || strings.Contains(reissued.Body.String(), adminEnrollment.Data.Token) {
		t.Fatalf("admin pairing reissue: %d %s", reissued.Code, reissued.Body.String())
	}
	canceled := adminRequest(http.MethodDelete, "/api/v1/agent-enrollments/"+adminEnrollment.Data.NodeID, "")
	if canceled.Code != http.StatusOK {
		t.Fatalf("admin pairing cancel: %d %s", canceled.Code, canceled.Body.String())
	}
	if _, err := nodes.Get(context.Background(), adminEnrollment.Data.NodeID); err == nil {
		t.Fatal("canceled pending API node still exists")
	}
	automaticRouter := NewRouter(Dependencies{Nodes: nodes, Agents: hub, Auth: adminAuth, Audit: audit.NewService(db)})
	automaticRequest := func(origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/agent-enrollments", strings.NewReader(`{"name":"automatic-pairing"}`))
		request.AddCookie(&http.Cookie{Name: sessionCookie, Value: adminToken})
		request.Header.Set("Origin", origin)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Forwarded-Proto", "https")
		response := httptest.NewRecorder()
		automaticRouter.ServeHTTP(response, request)
		return response
	}
	for _, origin := range []string{"", "http://example.com", "https://other.example.com"} {
		response := automaticRequest(origin)
		if response.Code == http.StatusCreated {
			t.Fatalf("Agent pairing unexpectedly accepted origin %q: %s", origin, response.Body.String())
		}
	}
	automatic := automaticRequest("https://example.com")
	if automatic.Code != http.StatusCreated || !strings.Contains(automatic.Body.String(), `"public_url":"https://example.com"`) {
		t.Fatalf("HTTPS page origin did not configure Agent pairing: %d %s", automatic.Code, automatic.Body.String())
	}
	var automaticEnrollment struct {
		Data struct {
			NodeID string `json:"node_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(automatic.Body.Bytes(), &automaticEnrollment); err != nil || automaticEnrollment.Data.NodeID == "" {
		t.Fatalf("invalid automatic pairing response: %s, %v", automatic.Body.String(), err)
	}
	reissueRequest := httptest.NewRequest(http.MethodPost, "/api/v1/agent-enrollments/"+automaticEnrollment.Data.NodeID+"/reissue", nil)
	reissueRequest.AddCookie(&http.Cookie{Name: sessionCookie, Value: adminToken})
	reissueRequest.Header.Set("Origin", "https://example.com")
	reissueResponse := httptest.NewRecorder()
	automaticRouter.ServeHTTP(reissueResponse, reissueRequest)
	if reissueResponse.Code != http.StatusOK || !strings.Contains(reissueResponse.Body.String(), `"public_url":"https://example.com"`) {
		t.Fatalf("HTTPS page origin did not configure Agent reissue: %d %s", reissueResponse.Code, reissueResponse.Body.String())
	}
	issued, err := nodes.IssueAgentEnrollment(context.Background(), node.AgentEnrollmentInput{Name: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/agents/enroll", strings.NewReader(`{"protocol":1,"version":"test"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+issued.Token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("enrollment failed: %d", response.StatusCode)
	}
	var result struct {
		Data struct {
			NodeID     string `json:"node_id"`
			Credential string `json:"credential"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Data.NodeID != issued.NodeID || result.Data.Credential == "" {
		t.Fatal("invalid credential exchange")
	}
	address := "ws" + strings.TrimPrefix(server.URL, "http")
	headers := http.Header{"Authorization": {"Bearer " + result.Data.Credential}, "X-SUMA-Agent-Node-ID": {issued.NodeID}, "X-SUMA-Agent-Protocol": {"1"}, "X-SUMA-Agent-Version": {"test"}}
	if _, response, err := websocket.DefaultDialer.Dial(address+"/ws/agents/control", http.Header{"Cookie": {sessionCookie + "=" + adminToken}}); err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("browser session reached Agent control channel: response=%v, err=%v", response, err)
	}
	browserHeaders := headers.Clone()
	browserHeaders.Set("Origin", "http://browser.example")
	if _, response, err := websocket.DefaultDialer.Dial(address+"/ws/agents/control", browserHeaders); err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("browser Origin reached Agent control channel: response=%v, err=%v", response, err)
	}
	control, _, err := websocket.DefaultDialer.Dial(address+"/ws/agents/control", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	go func() {
		for {
			_, payload, err := control.ReadMessage()
			if err != nil {
				return
			}
			var message struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			if json.Unmarshal(payload, &message) != nil || message.Type != "open" {
				return
			}
			go func() {
				backend, err := net.Dial("unix", dockerSocket)
				if err != nil {
					return
				}
				stream, _, err := websocket.DefaultDialer.Dial(address+"/ws/agents/streams/"+message.ID, headers)
				if err != nil {
					_ = backend.Close()
					return
				}
				_ = agentwire.Bridge(context.Background(), backend, stream)
			}()
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		view, err := nodes.Get(context.Background(), issued.NodeID)
		if err == nil && view.Status == "online" {
			client, err := nodes.Runtime(context.Background(), issued.NodeID)
			if err != nil {
				t.Fatal(err)
			}
			info, err := client.Info(context.Background())
			if err != nil || info.ID != "agent-engine" {
				t.Fatalf("Docker runtime failed through Agent: %+v, %v", info, err)
			}
			mismatch := database.Node{ID: "old-node", Name: "Old node", ConnectionType: node.ConnectionUnix, Endpoint: "unix:///missing/docker.sock", TLSMode: node.TLSDisabled, AllowedBindRootsJSON: "[]", EngineID: "different-engine", Enabled: true, Status: "offline"}
			if err := db.Create(&mismatch).Error; err != nil {
				t.Fatal(err)
			}
			oldEnrollment, err := nodes.IssueAgentEnrollment(context.Background(), node.AgentEnrollmentInput{NodeID: mismatch.ID, Name: mismatch.Name})
			if err != nil {
				t.Fatal(err)
			}
			_, oldSecret, err := nodes.ClaimAgentEnrollment(context.Background(), oldEnrollment.Token, agentwire.ProtocolVersion)
			if err != nil {
				t.Fatal(err)
			}
			oldHeaders := http.Header{"Authorization": {"Bearer " + oldSecret}, "X-SUMA-Agent-Node-ID": {mismatch.ID}, "X-SUMA-Agent-Protocol": {"1"}, "X-SUMA-Agent-Version": {"test"}}
			oldControl, _, err := websocket.DefaultDialer.Dial(address+"/ws/agents/control", oldHeaders)
			if err != nil {
				t.Fatal(err)
			}
			defer oldControl.Close()
			oldControlClosed := make(chan error, 1)
			go func() {
				for {
					_, payload, err := oldControl.ReadMessage()
					if err != nil {
						oldControlClosed <- err
						return
					}
					var message struct {
						Type string `json:"type"`
						ID   string `json:"id"`
					}
					if json.Unmarshal(payload, &message) != nil || message.Type != "open" {
						return
					}
					go func() {
						backend, err := net.Dial("unix", dockerSocket)
						if err != nil {
							return
						}
						stream, _, err := websocket.DefaultDialer.Dial(address+"/ws/agents/streams/"+message.ID, oldHeaders)
						if err != nil {
							_ = backend.Close()
							return
						}
						_ = agentwire.Bridge(context.Background(), backend, stream)
					}()
				}
			}()
			mismatchDeadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(mismatchDeadline) {
				old, _ := nodes.Get(context.Background(), mismatch.ID)
				if old.AgentEnrollment != nil && old.AgentEnrollment.LastError != "" {
					if old.ConnectionType != node.ConnectionUnix || old.EngineID != "different-engine" {
						t.Fatalf("failed migration changed existing node: %+v", old)
					}
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			old, _ := nodes.Get(context.Background(), mismatch.ID)
			if old.AgentEnrollment == nil || old.AgentEnrollment.LastError == "" {
				t.Fatal("Engine ID mismatch was not reported")
			}
			select {
			case err := <-oldControlClosed:
				if !websocket.IsCloseError(err, websocket.CloseInternalServerErr) {
					t.Fatalf("Agent activation failed without a WebSocket close reason: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Agent activation did not close the control connection")
			}
			duplicate, err := nodes.IssueAgentEnrollment(context.Background(), node.AgentEnrollmentInput{Name: "Duplicate Engine"})
			if err != nil {
				t.Fatal(err)
			}
			_, duplicateSecret, err := nodes.ClaimAgentEnrollment(context.Background(), duplicate.Token, agentwire.ProtocolVersion)
			if err != nil {
				t.Fatal(err)
			}
			duplicateHeaders := http.Header{"Authorization": {"Bearer " + duplicateSecret}, "X-SUMA-Agent-Node-ID": {duplicate.NodeID}, "X-SUMA-Agent-Protocol": {"1"}, "X-SUMA-Agent-Version": {"test"}}
			duplicateControl, _, err := websocket.DefaultDialer.Dial(address+"/ws/agents/control", duplicateHeaders)
			if err != nil {
				t.Fatal(err)
			}
			defer duplicateControl.Close()
			go func() {
				for {
					_, payload, err := duplicateControl.ReadMessage()
					if err != nil {
						return
					}
					var message struct {
						Type string `json:"type"`
						ID   string `json:"id"`
					}
					if json.Unmarshal(payload, &message) != nil || message.Type != "open" {
						return
					}
					go func() {
						backend, err := net.Dial("unix", dockerSocket)
						if err != nil {
							return
						}
						stream, _, err := websocket.DefaultDialer.Dial(address+"/ws/agents/streams/"+message.ID, duplicateHeaders)
						if err != nil {
							_ = backend.Close()
							return
						}
						_ = agentwire.Bridge(context.Background(), backend, stream)
					}()
				}
			}()
			duplicateDeadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(duplicateDeadline) {
				view, _ := nodes.Get(context.Background(), duplicate.NodeID)
				if view.AgentEnrollment != nil && view.AgentEnrollment.LastError != "" {
					if view.Status != "pairing" || view.EngineID != "" || !strings.Contains(view.AgentEnrollment.LastError, "already registered") {
						first, _ := nodes.Get(context.Background(), issued.NodeID)
						t.Fatalf("duplicate Engine activation changed node: %+v, enrollment error: %q, first node: %+v", view, view.AgentEnrollment.LastError, first)
					}
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			duplicateView, _ := nodes.Get(context.Background(), duplicate.NodeID)
			if duplicateView.AgentEnrollment == nil || duplicateView.AgentEnrollment.LastError == "" {
				t.Fatal("duplicate Engine ID was not rejected")
			}
			_ = control.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	view, _ := nodes.Get(context.Background(), issued.NodeID)
	t.Fatalf("Agent never became online: %+v", view)
}
