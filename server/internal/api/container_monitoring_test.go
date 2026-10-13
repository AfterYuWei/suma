package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	monitor "github.com/suma/suma/server/internal/containermonitor"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

func TestMonitoringHTTPAuthHistoryValidationAndAudit(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	service, err := monitor.New(db, tasks, monitor.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	account := auth.NewService(db, time.Hour)
	if _, err := account.Initialize(context.Background(), "admin", "admin@example.test", "", "strong-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := account.Login(context.Background(), "admin", "strong-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Auth: account, Audit: audit.NewService(db), Tasks: tasks, ContainerMonitoring: service})
	request := func(method, path string, body any, authenticated bool) *httptest.ResponseRecorder {
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost")
		if authenticated {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		}
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/monitoring/settings", nil}, {"PUT", "/api/v1/monitoring/settings", map[string]any{}}, {"GET", "/api/v1/monitoring/status", nil},
		{"GET", "/api/v1/monitoring/instances", nil}, {"POST", "/api/v1/monitoring/query", map[string]any{}},
		{"GET", "/api/v1/monitoring/instances/" + strings.Repeat("a", 64) + "/events", nil},
	} {
		if result := request(route.method, route.path, route.body, false); result.Code != 401 {
			t.Fatalf("unauthenticated %s = %d", route.path, result.Code)
		}
	}
	if result := request("POST", "/api/v1/monitoring/query", monitor.Query{}, true); result.Code != 400 {
		t.Fatal("invalid query accepted", result.Code)
	}
	if result := request("GET", "/api/v1/monitoring/instances?limit=500", nil, true); result.Code != 400 {
		t.Fatal("unbounded history list accepted")
	}
	p := service.Settings()
	p.RetentionDays = 1
	if result := request("PUT", "/api/v1/monitoring/settings", monitor.SavePolicy{Policy: p}, true); result.Code != 409 {
		t.Fatal("retention confirmation not enforced", result.Code)
	}
	p.Enabled = false
	result := request("PUT", "/api/v1/monitoring/settings", monitor.SavePolicy{Policy: p, ConfirmRetentionReduction: true}, true)
	if result.Code != 200 {
		t.Fatal("settings save failed", result.Code, result.Body.String())
	}
	var audits int64
	db.Model(&database.AuditLog{}).Where("action = 'monitoring.settings.update'").Count(&audits)
	if audits != 1 {
		t.Fatal("policy change not audited")
	}
	n := monitor.Node{ID: "removed-node", Name: "Removed", EngineID: "old-engine", Enabled: true}
	c := monitor.Inventory{ID: strings.Repeat("a", 64), Name: "removed-container"}
	if err := service.RecordInstance(context.Background(), n, c, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	id := monitor.InstanceID(n.ID, n.EngineID, c.ID)
	if result := request("GET", "/api/v1/monitoring/instances/"+id, nil, true); result.Code != 200 {
		t.Fatal("archived history required live Docker node")
	}
	target := monitor.Target{NodeID: n.ID, EngineID: n.EngineID, ContainerID: c.ID, Since: time.Now().Add(-time.Hour), Until: time.Now()}
	if result := request("POST", "/api/v1/monitoring/query", monitor.Query{Targets: []monitor.Target{target}}, true); result.Code != 200 {
		t.Fatal("offline query required Docker", result.Code)
	}
}
