package api

import (
	"context"
	"encoding/json"
	"github.com/suma/suma/server/internal/testutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

func TestAIModelDiscoveryHTTPAuthOriginAndDraftCredentials(t *testing.T) {
	dir := t.TempDir()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	authn := auth.NewService(db, time.Hour)
	if _, err = authn.Initialize(context.Background(), "admin", "models@test.example", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authn.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := ai.NewService(db, store, task.NewService(db), ai.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	var toolSupported atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer saved-model-secret" || r.Header.Get("Origin") != "" {
			t.Error("unexpected upstream request", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/custom/v1/models":
			w.Write([]byte(`{"data":[{"id":"model-b"},{"id":"model-a"}]}`))
		case r.Method == "POST" && r.URL.Path == "/custom/v1/responses":
			var input struct {
				Tools []any `json:"tools"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				t.Error("invalid Responses request")
				w.WriteHeader(400)
				return
			}
			if len(input.Tools) > 0 {
				if toolSupported.Load() {
					w.Write([]byte(`{"status":"completed","output":[{"type":"function_call","call_id":"probe","name":"connection_probe","arguments":"{\"message\":\"suma_connection_test\"}"}]}`))
					return
				}
				w.WriteHeader(400)
				w.Write([]byte(`{"error":{"message":"saved-model-secret"}}`))
				return
			}
			w.Write([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"Connected saved-model-secret"}]}]}`))
		default:
			t.Error("unexpected upstream endpoint", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer upstream.Close()
	cfg := assistant.Settings()
	cfg.Endpoint, cfg.Model, cfg.Models = upstream.URL+"/custom/v1", "model-a", []string{"model-a", "model-b"}
	cfg.AllowPrivate, cfg.AllowInsecure = true, true
	saved, err := assistant.SaveSettings(context.Background(), ai.SettingsInput{Settings: cfg, APIKey: "saved-model-secret"}, ai.Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Auth: authn, AI: assistant})
	request := func(method, path, origin, body string, authorized bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		if authorized {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	input := ai.ModelsInput{Version: saved.Version, Endpoint: saved.Endpoint, AllowPrivate: true, AllowInsecure: true}
	raw, _ := json.Marshal(input)
	if res := request("POST", "/ai/settings/models", "http://example.com", string(raw), false); res.Code != 401 {
		t.Fatal("unprotected discovery", res.Code)
	}
	if res := request("POST", "/ai/settings/models", "http://other.example.com", string(raw), true); res.Code != 403 || !strings.Contains(res.Body.String(), "Request origin is not allowed") {
		t.Fatal("insecure model permission bypassed CSRF", res.Code)
	}
	res := request("POST", "/ai/settings/models", "http://example.com", string(raw), true)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"models":["model-a","model-b"]`) || strings.Contains(res.Body.String(), "saved-model-secret") {
		t.Fatal("discovery failed", res.Code, res.Body.String())
	}
	res = request("GET", "/ai/settings", "", "", true)
	if res.Code != 200 || strings.Contains(res.Body.String(), "saved-model-secret") || !strings.Contains(res.Body.String(), `"models":["model-a","model-b"]`) {
		t.Fatal("settings response lost models or leaked key", res.Code)
	}
	res = request("POST", "/ai/settings/test", "http://example.com", "{}", true)
	if res.Code != 200 || strings.Contains(res.Body.String(), "saved-model-secret") {
		t.Fatal("connection result failed or exposed the key", res.Code, res.Body.String())
	}
	var tested struct {
		Data struct {
			Text         bool   `json:"text"`
			ToolCapable  bool   `json:"tool_capable"`
			SummaryOnly  bool   `json:"summary_only"`
			Model        string `json:"model"`
			TextResponse string `json:"text_response"`
			ToolFailure  string `json:"tool_failure"`
			ToolError    string `json:"tool_error"`
		}
	}
	if json.Unmarshal(res.Body.Bytes(), &tested) != nil || !tested.Data.Text || tested.Data.ToolCapable || !tested.Data.SummaryOnly || tested.Data.Model != "model-a" || tested.Data.TextResponse != "Connected [redacted]" || tested.Data.ToolFailure != "request_failed" || !strings.Contains(tested.Data.ToolError, "HTTP 400") {
		t.Fatal("connection result lost its failure details", res.Body.String())
	}
	toolSupported.Store(true)
	res = request("POST", "/ai/settings/test", "http://example.com", "{}", true)
	if res.Code != 200 || strings.Contains(res.Body.String(), "saved-model-secret") {
		t.Fatal("tool-capable connection failed or exposed the key", res.Code, res.Body.String())
	}
	tested.Data.ToolFailure, tested.Data.ToolError = "", ""
	if json.Unmarshal(res.Body.Bytes(), &tested) != nil || !tested.Data.Text || !tested.Data.ToolCapable || tested.Data.SummaryOnly || tested.Data.ToolFailure != "" || tested.Data.ToolError != "" || !assistant.Settings().ToolCapable {
		t.Fatal("valid Responses tool arguments did not pass over authenticated HTTP", res.Body.String())
	}
	for _, protocol := range []string{"chat_completions", "unknown", ""} {
		invalid := saved
		invalid.Protocol = protocol
		payload, _ := json.Marshal(invalid)
		res = request("PUT", "/ai/settings", "http://example.com", string(payload), true)
		if res.Code != 422 || !strings.Contains(res.Body.String(), "only Responses") {
			t.Fatal("unsupported protocol was accepted over HTTP", protocol, res.Code, res.Body.String())
		}
		if current := assistant.Settings(); current.Version != saved.Version || current.Protocol != ai.ProtocolResponses {
			t.Fatal("rejected protocol changed active settings")
		}
	}
	input.Version++
	raw, _ = json.Marshal(input)
	if res = request("POST", "/ai/settings/models", "http://example.com", string(raw), true); res.Code != 409 {
		t.Fatal("stale connection accepted", res.Code)
	}
}
