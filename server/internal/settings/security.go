package settings

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// SecurityPolicy is immutable after publication by Service.
type SecurityPolicy struct {
	BrowserOrigin  string
	TrustedProxies []netip.Prefix
}

func parseSecurity(values map[string]string) (*SecurityPolicy, error) {
	policy := &SecurityPolicy{}
	if value := strings.TrimSpace(values["security.browser_origin"]); value != "" {
		origin, err := ParseOrigin(value)
		if err != nil {
			return nil, fmt.Errorf("browser origin: %w", err)
		}
		policy.BrowserOrigin = origin
	}
	if value := strings.TrimSpace(values["security.trusted_proxies"]); value != "" {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			var prefix netip.Prefix
			if address, err := netip.ParseAddr(item); err == nil {
				address = address.Unmap()
				prefix = netip.PrefixFrom(address, address.BitLen())
			} else {
				prefix, err = netip.ParsePrefix(item)
				if err != nil {
					return nil, fmt.Errorf("trusted proxies: invalid IP or CIDR %q", item)
				}
				prefix = prefix.Masked()
			}
			if prefix.Bits() == 0 {
				return nil, errors.New("trusted proxies: trusting every address is not allowed")
			}
			policy.TrustedProxies = append(policy.TrustedProxies, prefix)
		}
	}
	return policy, nil
}

// ParseOrigin accepts only a serialized HTTP(S) origin, without a path or credentials.
func ParseOrigin(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return "", errors.New("use an http:// or https:// origin without a path")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return "", errors.New("use an http:// or https:// origin without a path")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", errors.New("origin port is invalid")
		}
	}
	return parsed.Scheme + "://" + strings.ToLower(parsed.Host), nil
}

func (p *SecurityPolicy) AllowsOrigin(request *http.Request) bool {
	origin, err := ParseOrigin(request.Header.Get("Origin"))
	if err != nil {
		return false
	}
	parsed, _ := url.Parse(origin)
	if !strings.EqualFold(parsed.Host, request.Host) {
		return false
	}
	return p == nil || p.BrowserOrigin == "" || origin == p.BrowserOrigin
}

func (p *SecurityPolicy) ClientIP(request *http.Request) string {
	remote := request.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	address, err := netip.ParseAddr(remote)
	if err != nil {
		return remote
	}
	address = address.Unmap()
	if p == nil || !p.trusts(address) {
		return address.String()
	}
	forwarded := strings.Join(request.Header.Values("X-Forwarded-For"), ",")
	if forwarded == "" {
		return address.String()
	}
	parts := strings.Split(forwarded, ",")
	for index := len(parts) - 1; index >= 0; index-- {
		next, err := netip.ParseAddr(strings.TrimSpace(parts[index]))
		if err != nil {
			return address.String()
		}
		address = next.Unmap()
		if !p.trusts(address) {
			return address.String()
		}
	}
	return address.String()
}

func (p *SecurityPolicy) trusts(address netip.Addr) bool {
	for _, prefix := range p.TrustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
