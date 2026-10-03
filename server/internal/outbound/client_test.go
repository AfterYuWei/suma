package outbound

import (
	"net"
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
