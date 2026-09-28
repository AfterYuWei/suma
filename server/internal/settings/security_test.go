package settings

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestSecurityOriginValidation(t *testing.T) {
	for _, value := range []string{"https://app.example.test/", "https://user@app.example.test", "https://app.example.test/path", "https://app.example.test?x=1", "null", "https://app.example.test:70000"} {
		if _, err := ParseOrigin(value); err == nil {
			t.Errorf("accepted invalid origin %q", value)
		}
	}
	request := httptest.NewRequest("POST", "http://app.example.test:8080/api/v1/settings", nil)
	for _, row := range []struct {
		origin string
		allow  bool
	}{
		{"http://app.example.test:8080", true},
		{"https://app.example.test:8080", true},
		{"https://other.app.example.test:8080", false},
		{"https://app.example.test.evil.test:8080", false},
		{"https://app.example.test:8081", false},
		{"", false},
	} {
		request.Header.Set("Origin", row.origin)
		if got := (&SecurityPolicy{}).AllowsOrigin(request); got != row.allow {
			t.Errorf("fallback origin %q: got %t, want %t", row.origin, got, row.allow)
		}
	}
	request.Header.Set("Origin", "http://app.example.test:8080")
	if !(&SecurityPolicy{BrowserOrigin: "http://app.example.test:8080"}).AllowsOrigin(request) {
		t.Fatal("configured matching origin was rejected")
	}
	request.Header.Set("Origin", "https://app.example.test:8080")
	if (&SecurityPolicy{BrowserOrigin: "http://app.example.test:8080"}).AllowsOrigin(request) {
		t.Fatal("configured origin accepted a different scheme")
	}
}

func TestSecurityClientIPTrustedProxyChain(t *testing.T) {
	policy, err := parseSecurity(map[string]string{"security.trusted_proxies": "192.0.2.10, 10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://example.test/", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.99, 198.51.100.7, 10.0.0.2")
	if got := (&SecurityPolicy{}).ClientIP(request); got != "192.0.2.10" {
		t.Fatalf("untrusted header produced %q", got)
	}
	if got := policy.ClientIP(request); got != "198.51.100.7" {
		t.Fatalf("trusted chain produced %q", got)
	}
	request.RemoteAddr = "198.51.100.8:1234"
	if got := policy.ClientIP(request); got != "198.51.100.8" {
		t.Fatalf("untrusted peer produced %q", got)
	}
	for _, value := range []string{"0.0.0.0/0", "::/0", "bad-host", "192.0.2.1,"} {
		if _, err := parseSecurity(map[string]string{"security.trusted_proxies": value}); err == nil {
			t.Errorf("accepted invalid trusted proxies %q", value)
		}
	}
}

func TestSecuritySettingsApplyImmediatelyAndRollbackTogether(t *testing.T) {
	db := openDatabase(t, filepath.Join(t.TempDir(), "settings.db"))
	service := NewService(db, testConfig())
	service.defaults["security.browser_origin"] = "http://env.example.test"
	if err := service.LoadSecurity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := service.Security().BrowserOrigin; got != "http://env.example.test" {
		t.Fatalf("environment initial value = %q", got)
	}
	_, err := service.Update(context.Background(), map[string]string{"security.browser_origin": "", "security.trusted_proxies": "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	if got := service.Security().BrowserOrigin; got != "" {
		t.Fatalf("saved blank did not override environment: %q", got)
	}
	if len(service.Security().TrustedProxies) != 1 {
		t.Fatal("trusted proxy change was not active immediately")
	}
	before := service.Security()
	if _, err := service.Update(context.Background(), map[string]string{"general.server_name": "Should not save", "security.trusted_proxies": "0.0.0.0/0"}); err == nil {
		t.Fatal("invalid security setting was accepted")
	}
	if service.Security() != before {
		t.Fatal("invalid update changed the active policy")
	}
	stored, err := service.Get(context.Background())
	if err != nil || stored["general.server_name"] != "SUMA" {
		t.Fatalf("invalid update changed stored settings: %v, %#v", err, stored)
	}
}
