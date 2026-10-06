package outbound

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestForbiddenNetworksAndURLPolicy(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.2", "169.254.169.254", "::1", "fe80::1", "0.0.0.0", "224.0.0.1"} {
		if SafeIP(net.ParseIP(ip), false) {
			t.Fatal("unsafe public endpoint", ip)
		}
	}
	if SafeIP(net.ParseIP("169.254.169.254"), true) {
		t.Fatal("metadata endpoint allowed")
	}
	if !SafeIP(net.ParseIP("10.0.0.2"), true) {
		t.Fatal("explicit private endpoint rejected")
	}
	for _, u := range []string{"file:///etc/passwd", "https://user:pass@example.com", "http://example.com", "https://example.com/#secret"} {
		if Validate(u, false) == nil {
			t.Fatal("invalid endpoint accepted", u)
		}
	}
}

func TestModelHTTPRequiresIndependentPrivateAndInsecurePermissions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	for _, private := range []bool{false, true} {
		for _, insecure := range []bool{false, true} {
			res, err := ModelClient(private, insecure).Get(server.URL)
			if res != nil {
				res.Body.Close()
			}
			if (err == nil) != (private && insecure) {
				t.Fatalf("private=%v insecure=%v: %v", private, insecure, err)
			}
		}
	}
}

func TestInsecureModelPermissionRetainsTLSAndRedirectChecks(t *testing.T) {
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer tls.Close()
	if res, err := ModelClient(true, true).Get(tls.URL); err == nil {
		res.Body.Close()
		t.Fatal("untrusted TLS certificate accepted")
	}
	var targetReached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetReached.Store(true) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	if res, err := ModelClient(true, true).Get(redirect.URL); err == nil {
		res.Body.Close()
		t.Fatal("redirect accepted")
	} else if res != nil {
		res.Body.Close()
	}
	if targetReached.Load() {
		t.Fatal("credentials could reach redirect destination")
	}
}
