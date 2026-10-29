package web

import (
	"crypto/tls"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func testPrefixes(t *testing.T, values ...string) []netip.Prefix {
	t.Helper()
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			t.Fatalf("ParsePrefix(%q): %v", value, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

func TestClientIPIgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	request := httptest.NewRequest("GET", "http://example.com/", nil)
	request.RemoteAddr = "203.0.113.9:43120"
	request.Header.Set("X-Forwarded-For", "198.51.100.7")

	if got := clientIP(request, testPrefixes(t, "127.0.0.1/32")); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want direct peer", got)
	}
}

func TestClientIPWalksTrustedChainFromRight(t *testing.T) {
	request := httptest.NewRequest("GET", "http://example.com/", nil)
	request.RemoteAddr = "127.0.0.1:43120"
	request.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7, 10.0.0.2")
	trusted := testPrefixes(t, "127.0.0.1/32", "10.0.0.0/8")

	if got := clientIP(request, trusted); got != "198.51.100.7" {
		t.Fatalf("clientIP = %q, want first untrusted hop", got)
	}
}

func TestClientIPAllTrustedFallsBackToLeftmost(t *testing.T) {
	request := httptest.NewRequest("GET", "http://example.com/", nil)
	request.RemoteAddr = "127.0.0.1:43120"
	request.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	trusted := testPrefixes(t, "127.0.0.1/32", "10.0.0.0/8")

	if got := clientIP(request, trusted); got != "10.0.0.1" {
		t.Fatalf("clientIP = %q, want leftmost trusted address", got)
	}
}

func TestClientIPSkipsMalformedForwardedEntries(t *testing.T) {
	request := httptest.NewRequest("GET", "http://example.com/", nil)
	request.RemoteAddr = "127.0.0.1:43120"
	request.Header.Set("X-Forwarded-For", "not-an-ip, 198.51.100.7")

	if got := clientIP(request, testPrefixes(t, "127.0.0.1/32")); got != "198.51.100.7" {
		t.Fatalf("clientIP = %q, want valid client entry", got)
	}
}

func TestClientIPIPv6(t *testing.T) {
	request := httptest.NewRequest("GET", "http://example.com/", nil)
	request.RemoteAddr = "[::1]:43120"
	request.Header.Set("X-Forwarded-For", "2001:db8::7")
	trusted := testPrefixes(t, "::1/128")

	if got := clientIP(request, trusted); got != "2001:db8::7" {
		t.Fatalf("clientIP = %q, want IPv6 client", got)
	}
}

func TestRequestScheme(t *testing.T) {
	trusted := testPrefixes(t, "127.0.0.1/32")

	request := httptest.NewRequest("GET", "http://example.com/", nil)
	request.RemoteAddr = "127.0.0.1:43120"
	request.Header.Set("X-Forwarded-Proto", "https")
	if got := requestScheme(request, trusted); got != "https" {
		t.Fatalf("requestScheme = %q, want https", got)
	}

	request.RemoteAddr = "203.0.113.9:43120"
	if got := requestScheme(request, trusted); got != "http" {
		t.Fatalf("requestScheme = %q, want ignored header", got)
	}

	request.RemoteAddr = "203.0.113.9:43120"
	request.TLS = &tls.ConnectionState{}
	if got := requestScheme(request, trusted); got != "https" {
		t.Fatalf("requestScheme = %q, want TLS https", got)
	}

	request = httptest.NewRequest("GET", "http://example.com/", nil)
	request.RemoteAddr = "127.0.0.1:43120"
	request.Header.Set("X-Forwarded-Proto", "https, http")
	if got := requestScheme(request, trusted); got != "http" {
		t.Fatalf("requestScheme = %q, want closest hop", got)
	}
}

func TestRequestHost(t *testing.T) {
	trusted := testPrefixes(t, "127.0.0.1/32")

	request := httptest.NewRequest("GET", "http://internal:8080/", nil)
	request.RemoteAddr = "127.0.0.1:43120"
	request.Header.Set("X-Forwarded-Host", "status.example.com")
	if got := requestHost(request, trusted); got != "status.example.com" {
		t.Fatalf("requestHost = %q, want forwarded host", got)
	}

	request.RemoteAddr = "203.0.113.9:43120"
	if got := requestHost(request, trusted); got != "internal:8080" {
		t.Fatalf("requestHost = %q, want original host", got)
	}

	request.RemoteAddr = "127.0.0.1:43120"
	request.Header.Set("X-Forwarded-Host", "bad host")
	if got := requestHost(request, trusted); got != "internal:8080" {
		t.Fatalf("requestHost = %q, want sanitized fallback", got)
	}

	request.Header.Set("X-Forwarded-Host", "attacker@example.com")
	if got := requestHost(request, trusted); got != "internal:8080" {
		t.Fatalf("requestHost = %q, want userinfo host rejected", got)
	}

	request.Header.Set("X-Forwarded-Host", "old.example.com, status.example.com")
	if got := requestHost(request, trusted); got != "status.example.com" {
		t.Fatalf("requestHost = %q, want closest hop", got)
	}
}
