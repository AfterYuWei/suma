// Package outbound constrains requests to explicitly configured destinations.
package outbound

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func Validate(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("invalid endpoint URL")
	}
	if u.Scheme != "https" && !(allowPrivate && u.Scheme == "http") {
		return errors.New("endpoint requires verified HTTPS; internal HTTP must be explicitly allowed")
	}
	if strings.Contains(u.Hostname(), "%") {
		return errors.New("invalid endpoint host")
	}
	return nil
}
func SafeIP(ip net.IP, allowPrivate bool) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if !allowPrivate && (ip.IsPrivate() || ip.IsLoopback()) {
		return false
	}
	return true
}
func Client(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Avoid proxy-based bypasses of destination validation.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, errors.New("endpoint DNS resolution failed")
		}
		for _, candidate := range ips {
			if !SafeIP(candidate.IP, allowPrivate) || ctx.Value(plainHTTPKey{}) == true && !candidate.IP.IsPrivate() && !candidate.IP.IsLoopback() {
				return nil, errors.New("endpoint resolves to a forbidden network address")
			}
		}
		for _, candidate := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("endpoint connection failed")
	}
	return &http.Client{Transport: guardedTransport{base: transport, allowPrivate: allowPrivate}, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("endpoint redirects are not allowed") }}
}
