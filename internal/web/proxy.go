package web

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP returns the effective client address. Forwarded headers are only
// honored when the direct peer is one of the configured trusted proxies.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	remote, ok := parseRemoteAddress(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	remote = remote.Unmap()
	if !isTrustedAddress(remote, trusted) {
		return remote.String()
	}
	chain, ok := forwardedChain(r)
	if !ok || len(chain) == 0 {
		return remote.String()
	}
	for index := len(chain) - 1; index >= 0; index-- {
		if !isTrustedAddress(chain[index], trusted) {
			return chain[index].String()
		}
	}
	return chain[0].String()
}

func (s *Server) requestScheme(r *http.Request) string {
	return requestScheme(r, s.trustedProxies)
}

func (s *Server) requestHost(r *http.Request) string {
	return requestHost(r, s.trustedProxies)
}

func requestScheme(r *http.Request, trusted []netip.Prefix) string {
	if remote, ok := parseRemoteAddress(r.RemoteAddr); ok && isTrustedAddress(remote.Unmap(), trusted) {
		var tokens []string
		for _, value := range r.Header.Values("X-Forwarded-Proto") {
			for _, part := range strings.Split(value, ",") {
				tokens = append(tokens, strings.ToLower(strings.TrimSpace(part)))
			}
		}
		// The closest hop is the rightmost value. Appending proxies must not let
		// a client-supplied first value decide cookie security.
		for index := len(tokens) - 1; index >= 0; index-- {
			switch tokens[index] {
			case "http":
				return "http"
			case "https":
				return "https"
			}
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func requestHost(r *http.Request, trusted []netip.Prefix) string {
	if remote, ok := parseRemoteAddress(r.RemoteAddr); ok && isTrustedAddress(remote.Unmap(), trusted) {
		var hosts []string
		for _, value := range r.Header.Values("X-Forwarded-Host") {
			hosts = append(hosts, strings.Split(value, ",")...)
		}
		for index := len(hosts) - 1; index >= 0; index-- {
			if host := sanitizeHost(hosts[index]); host != "" {
				return host
			}
		}
	}
	return r.Host
}

func parseRemoteAddress(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Addr{}, false
	}
	if addrPort, err := netip.ParseAddrPort(value); err == nil {
		return addrPort.Addr(), true
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
			return addr, true
		}
	}
	if addr, err := netip.ParseAddr(strings.Trim(value, "[]")); err == nil {
		return addr, true
	}
	return netip.Addr{}, false
}

func forwardedChain(r *http.Request) ([]netip.Addr, bool) {
	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return nil, true
	}
	var chain []netip.Addr
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			addr, ok := parseForwardedAddress(part)
			if !ok {
				// Ignore malformed entries instead of failing the whole chain.
				// A client can otherwise force every request behind an appending
				// proxy onto the proxy IP's login-rate-limit bucket.
				continue
			}
			chain = append(chain, addr)
		}
	}
	return chain, true
}

func parseForwardedAddress(value string) (netip.Addr, bool) {
	if addrPort, err := netip.ParseAddrPort(value); err == nil {
		return addrPort.Addr().Unmap(), true
	}
	value = strings.Trim(value, "[]")
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.Unmap(), true
	}
	return netip.Addr{}, false
}

func isTrustedAddress(addr netip.Addr, trusted []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func sanitizeHost(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/\\@?#") {
		return ""
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return ""
		}
	}
	return value
}
