package api

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestContainerObservationHTTPValidationAndIsolation(t *testing.T) {
	id := strings.Repeat("a", 64)
	_, _, request := overviewHTTPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := projectDockerVersion.ReplaceAllString(r.URL.Path, "")
		switch path {
		case "/containers/" + id + "/json":
			writeProjectDockerJSON(w, map[string]any{"Id": id, "Config": map[string]any{}, "State": map[string]any{"Status": "running", "Running": true, "OOMKilled": false}, "HostConfig": map[string]any{"Memory": 0}, "RestartCount": 0})
		case "/events":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
		default:
			overviewEngineJSON(w, r, path)
		}
	}))
	for _, check := range []struct {
		path   string
		auth   bool
		status int
	}{
		{"/api/v1/nodes/local/containers/" + id + "/runtime", false, 401},
		{"/api/v1/nodes/missing/containers/" + id + "/runtime", true, 404},
		{"/api/v1/nodes/local/containers/" + id + "/runtime", true, 200},
		{"/api/v1/nodes/local/containers/" + id + "/events", true, 200},
		{"/api/v1/nodes/local/containers/short/events", true, 400},
		{"/api/v1/nodes/local/containers/" + id + "/events?limit=257", true, 400},
		{"/api/v1/nodes/local/containers/" + id + "/events?since=invalid", true, 400},
		{"/api/v1/nodes/local/containers/" + id + "/events?since=2026-01-01T00:00:00Z&until=2026-01-03T00:00:00Z", true, 400},
	} {
		r := request(context.Background(), check.path, check.auth)
		if r.Code != check.status {
			t.Fatalf("%s: got %d expected %d (%s)", check.path, r.Code, check.status, r.Body.String())
		}
	}
}

func TestContainerObservationWebSocketSecurityAndDisconnect(t *testing.T) {
	id := strings.Repeat("b", 64)
	eventCanceled, inspectStarted, inspectCanceled := make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 1)
	router, _, _ := overviewHTTPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := projectDockerVersion.ReplaceAllString(r.URL.Path, "")
		switch path {
		case "/events":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"Type": "container", "Action": "start", "timeNano": time.Now().UnixNano(), "Actor": map[string]any{"ID": id, "Attributes": map[string]string{"password": "private-value"}}})
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			eventCanceled <- struct{}{}
		case "/containers/" + id + "/json":
			inspectStarted <- struct{}{}
			<-r.Context().Done()
			inspectCanceled <- struct{}{}
		default:
			overviewEngineJSON(w, r, path)
		}
	}))
	server := httptest.NewServer(router)
	defer server.Close()
	login, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"long-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", server.URL)
	response, err := http.DefaultClient.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("login failed: %d", response.StatusCode)
	}
	headers := http.Header{"Origin": []string{server.URL}}
	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookie {
			headers.Set("Cookie", cookie.String())
		}
	}
	address := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws/nodes/local/containers/" + id + "/events"
	for _, check := range []struct {
		headers http.Header
		status  int
	}{{http.Header{"Origin": []string{server.URL}}, 401}, {http.Header{"Origin": []string{"https://invalid.example"}, "Cookie": headers.Values("Cookie")}, 403}} {
		conn, res, err := websocket.DefaultDialer.Dial(address, check.headers)
		if conn != nil {
			conn.Close()
		}
		if res != nil {
			res.Body.Close()
		}
		if err == nil || res == nil || res.StatusCode != check.status {
			t.Fatalf("invalid socket handshake was not rejected: %v", err)
		}
	}
	conn, _, err := websocket.DefaultDialer.Dial(address, headers)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, payload, err := conn.ReadMessage()
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "private-value") {
		t.Fatal("event attributes leaked")
	}
	select {
	case <-eventCanceled:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not close Docker events")
	}
	statsAddress := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws/nodes/local/containers/" + id + "/stats?format=normalized"
	conn, _, err = websocket.DefaultDialer.Dial(statsAddress, headers)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-inspectStarted:
	case <-time.After(time.Second):
		conn.Close()
		t.Fatal("statistics inspection did not start")
	}
	conn.Close()
	select {
	case <-inspectCanceled:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel statistics opening")
	}
}
