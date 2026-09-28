package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/config"
	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/settings"
	"github.com/suma/suma/server/internal/task"
)

func TestSecurityBoundarySettingsApplyImmediately(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "security.db"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(db, time.Hour)
	if _, err := authService.Initialize(context.Background(), "admin", "admin@example.test", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authService.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	usernameKey, err := authService.LoginPrincipalKey(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	emailKey, err := authService.LoginPrincipalKey(context.Background(), "admin@example.test")
	if err != nil || usernameKey != emailKey {
		t.Fatalf("username and email rate-limit keys differ: %q, %q, %v", usernameKey, emailKey, err)
	}
	settingsService := settings.NewService(db, config.Config{})
	if err := settingsService.LoadSecurity(context.Background()); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Auth: authService, Audit: audit.NewService(db), Tasks: task.NewService(db), Settings: settingsService})
	request := func(origin, contentType, body, forwarded string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
		req.RemoteAddr = "192.0.2.10:1234"
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Forwarded-For", forwarded)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	change := `{"general.server_name":"Test"}`
	for _, row := range []struct {
		origin, contentType string
		status              int
	}{
		{"", "application/json", http.StatusForbidden},
		{"http://other.example.com", "application/json", http.StatusForbidden},
		{"http://example.com.evil.test", "application/json", http.StatusForbidden},
		{"http://example.com:8080", "application/json", http.StatusForbidden},
		{"http://example.com", "text/plain", http.StatusUnsupportedMediaType},
		{"https://example.com", "application/json", http.StatusOK},
	} {
		response := request(row.origin, row.contentType, change, "203.0.113.1")
		if response.Code != row.status {
			t.Errorf("origin=%q type=%q: %d %s, want %d", row.origin, row.contentType, response.Code, response.Body.String(), row.status)
		}
	}
	if response := request("http://example.com", "application/json", `{"security.browser_origin":"https://example.com"}`, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("different page origin was accepted: %d %s", response.Code, response.Body.String())
	}
	if response := request("http://example.com", "application/json", `{"security.browser_origin":"http://example.com","security.trusted_proxies":"192.0.2.10"}`, "203.0.113.1"); response.Code != http.StatusOK {
		t.Fatalf("security settings save: %d %s", response.Code, response.Body.String())
	}
	if response := request("https://example.com", "application/json", change, "203.0.113.1"); response.Code != http.StatusForbidden {
		t.Fatalf("old fallback remained active: %d %s", response.Code, response.Body.String())
	}
	if response := request("http://example.com", "application/json", change, "203.0.113.1"); response.Code != http.StatusOK {
		t.Fatalf("configured origin failed: %d %s", response.Code, response.Body.String())
	}
	var latest database.AuditLog
	if err := db.Order("id DESC").First(&latest).Error; err != nil || latest.IP != "203.0.113.1" {
		t.Fatalf("trusted proxy did not update audit IP immediately: %#v, %v", latest, err)
	}
}

func TestWebSocketOriginCheckRejectsMissingAndSubstring(t *testing.T) {
	for _, row := range []struct {
		origin string
		allow  bool
	}{
		{"", false},
		{"http://example.com.evil.test", false},
		{"http://other.example.com", false},
		{"http://example.com", true},
	} {
		request := httptest.NewRequest(http.MethodGet, "http://example.com/ws/tasks/example", nil)
		request.Header.Set("Origin", row.origin)
		if got := upgrader.CheckOrigin(request); got != row.allow {
			t.Errorf("WebSocket origin %q: %t, want %t", row.origin, got, row.allow)
		}
	}
}

type echoTerminal struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func (t *echoTerminal) Read(value []byte) (int, error)  { return t.reader.Read(value) }
func (t *echoTerminal) Write(value []byte) (int, error) { return t.writer.Write(value) }
func (t *echoTerminal) Close() error {
	_ = t.writer.Close()
	return t.reader.Close()
}
func (t *echoTerminal) Resize(context.Context, uint, uint) error { return nil }

type terminalOnlyContainers struct{ containerdomain.Service }

func (terminalOnlyContainers) Terminal(context.Context, string, uint, uint) (containerdomain.Terminal, error) {
	reader, writer := io.Pipe()
	return &echoTerminal{reader: reader, writer: writer}, nil
}

func TestWebSocketTerminalHandshakeAndEcho(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "terminal.db"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(db, time.Hour)
	if _, err := authService.Initialize(context.Background(), "admin", "admin@example.test", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authService.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	securitySettings := settings.NewService(db, config.Config{})
	if err := securitySettings.LoadSecurity(context.Background()); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Containers: terminalOnlyContainers{}, Auth: authService, Audit: audit.NewService(db), Tasks: task.NewService(db), Settings: securitySettings})
	server := httptest.NewServer(router)
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := securitySettings.Update(context.Background(), map[string]string{"security.browser_origin": server.URL}); err != nil {
		t.Fatal(err)
	}
	address := "ws://" + parsed.Host + "/ws/containers/example/terminal"
	dial := func(origin string) (*websocket.Conn, *http.Response, error) {
		headers := http.Header{}
		headers.Set("Cookie", sessionCookie+"="+token)
		if origin != "" {
			headers.Set("Origin", origin)
		}
		return websocket.DefaultDialer.Dial(address, headers)
	}
	for _, origin := range []string{"", "http://evil.example.test", "https://" + parsed.Host} {
		connection, response, err := dial(origin)
		if connection != nil {
			_ = connection.Close()
		}
		if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
			t.Fatalf("origin %q: response=%v error=%v", origin, response, err)
		}
	}
	connection, _, err := dial(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := connection.WriteMessage(websocket.BinaryMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	messageType, message, err := connection.ReadMessage()
	if err != nil || messageType != websocket.BinaryMessage || string(message) != "hello" {
		t.Fatalf("terminal echo: type=%d value=%q error=%v", messageType, message, err)
	}
}

func TestLoginLimiterByIPAndAccountRecovers(t *testing.T) {
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	limiter := newLoginLimiter()
	limiter.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		if delay := limiter.admitIP("192.0.2.1"); delay != 0 {
			t.Fatalf("attempt %d limited too soon: %s", i, delay)
		}
		if delay := limiter.admitAccount("user:1"); delay != 0 {
			t.Fatalf("account attempt %d limited too soon: %s", i, delay)
		}
	}
	if delay := limiter.admitIP("192.0.2.1"); delay != time.Minute {
		t.Fatalf("IP limit delay = %s", delay)
	}
	now = now.Add(time.Minute)
	for i := 0; i < 10; i++ {
		if delay := limiter.admitIP("192.0.2.2"); delay != 0 {
			t.Fatalf("cross-IP attempt %d limited too soon: %s", i, delay)
		}
		if delay := limiter.admitAccount("user:1"); delay != 0 {
			t.Fatalf("account attempt %d limited too soon: %s", i, delay)
		}
	}
	if delay := limiter.admitAccount("user:1"); delay != 14*time.Minute {
		t.Fatalf("account limit delay = %s", delay)
	}
	now = now.Add(14 * time.Minute)
	if delay := limiter.admitAccount("user:1"); delay != 0 {
		t.Fatalf("account did not recover: %s", delay)
	}
}

func TestPasswordLoginHTTPRateLimitIgnoresSpoofedForwardedIP(t *testing.T) {
	router := testRouter(t, &fakeEngine{})
	for attempt := 0; attempt < 11; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(attempt+1))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if attempt < 10 && response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d %s", attempt, response.Code, response.Body.String())
		}
		if attempt == 10 && (response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "") {
			t.Fatalf("rate limit: %d %s, Retry-After=%q", response.Code, response.Body.String(), response.Header().Get("Retry-After"))
		}
	}
}
