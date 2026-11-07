package api

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/projectlogs"
	"github.com/suma/suma/server/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type operationRuntime struct{ active atomic.Int32 }

func (r *operationRuntime) ProjectLogSources(context.Context, string) ([]projectlogs.Source, error) {
	return []projectlogs.Source{{ContainerID: "container", ContainerName: "shop-web-1", Service: "web"}}, nil
}
func (r *operationRuntime) ReadProjectLogs(ctx context.Context, source projectlogs.Source, q projectlogs.Query, emit func(projectlogs.Record) error) error {
	r.active.Add(1)
	defer r.active.Add(-1)
	if q.Follow {
		<-ctx.Done()
		return ctx.Err()
	}
	return emit(projectlogs.Record{ID: "record", ContainerID: source.ContainerID, Time: time.Now().UTC(), Service: source.Service, Stream: "stdout", Text: "hello"})
}

func TestOperationsHTTPAndWebSocketIsolation(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	sql.SetMaxOpenConns(1)
	authentication := auth.NewService(db, time.Hour)
	if _, err := authentication.Initialize(context.Background(), "admin", "admin@test.example", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authentication.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &operationRuntime{}
	logs := projectlogs.NewService(projectlogs.Dependencies{Runtime: func(context.Context, string) (projectlogs.Runtime, error) { return runtime, nil }, Exists: func(_ context.Context, id, name string) error {
		if id != "local" || name != "shop" {
			return projectlogs.ErrInvalid
		}
		return nil
	}})
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Auth: authentication, ProjectLogs: logs})
	request := func(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://suma.test"+path, strings.NewReader(body))
		if authenticated {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		}
		req.Header.Set("Origin", "http://suma.test")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	if w := request("GET", "/api/v1/nodes/local/projects/compose/shop/log-sources", "", false); w.Code != 401 {
		t.Fatalf("auth %d", w.Code)
	}
	w := request("GET", "/api/v1/nodes/local/projects/compose/shop/logs/history?tail=200", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"text":"hello"`) {
		t.Fatalf("history %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/v1/nodes/local/projects/compose/shop/logs/history?containers=foreign", "", true); w.Code != 400 {
		t.Fatalf("foreign container %d", w.Code)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	address := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/nodes/local/projects/compose/shop/logs?tail=200"
	headers := http.Header{"Cookie": []string{sessionCookie + "=" + token}, "Origin": []string{server.URL}}
	headers.Set("Origin", "http://foreign.example")
	if connection, _, err := websocket.DefaultDialer.Dial(address, headers); err == nil {
		connection.Close()
		t.Fatal("cross origin socket")
	}
	headers.Set("Origin", server.URL)
	connection, _, err := websocket.DefaultDialer.Dial(address, headers)
	if err != nil {
		t.Fatal(err)
	}
	var event projectlogs.Event
	connection.SetReadDeadline(time.Now().Add(time.Second))
	if err := connection.ReadJSON(&event); err != nil || event.Type != "snapshot" {
		t.Fatalf("snapshot %+v %v", event, err)
	}
	connection.Close()
	deadline := time.Now().Add(time.Second)
	for runtime.active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond * 10)
	}
	if runtime.active.Load() != 0 {
		t.Fatal("Docker stream leaked after disconnect")
	}
	raw, _ := json.Marshal(event)
	if !strings.Contains(string(raw), "container") {
		t.Fatal("source metadata missing")
	}
}
