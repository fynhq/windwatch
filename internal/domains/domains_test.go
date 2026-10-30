package domains

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/proxy"
	"github.com/fynhq/windwatch/internal/store"
)

const rdapPayload = `{
	"status": ["clientTransferProhibited"],
	"events": [
		{"eventAction": "registration", "eventDate": "2020-01-01T00:00:00Z"},
		{"eventAction": "expiration", "eventDate": "2027-01-01T00:00:00Z"}
	],
	"entities": [
		{"roles": ["registrar"], "vcardArray": ["vcard", [["fn", {}, "text", "Example Registrar"]]]}
	]
}`

func writeRDAP(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/rdap+json")
	_, _ = w.Write([]byte(rdapPayload))
}

func noProxy(t *testing.T) func() (*proxy.Config, error) {
	t.Helper()
	return func() (*proxy.Config, error) { return nil, nil }
}

func fastRetries(t *testing.T) {
	t.Helper()
	delays := rdapRetryDelays
	rdapRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { rdapRetryDelays = delays })
}

func useFallbackEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	previous := rdapFallbackEndpoint
	rdapFallbackEndpoint = endpoint
	t.Cleanup(func() { rdapFallbackEndpoint = previous })
}

func useBootstrapServices(t *testing.T, services map[string][]string) {
	t.Helper()
	rdapBootstrap.reset()
	rdapBootstrap.store(services)
	t.Cleanup(rdapBootstrap.reset)
}

func useBootstrapURL(t *testing.T, endpoint string) {
	t.Helper()
	previous := rdapBootstrapURL
	rdapBootstrapURL = endpoint
	rdapBootstrap.reset()
	t.Cleanup(func() {
		rdapBootstrapURL = previous
		rdapBootstrap.reset()
	})
}

func newDomainStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "windwatch.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func TestDomainForURL(t *testing.T) {
	tests := []struct {
		url     string
		want    string
		wantErr bool
	}{
		{url: "https://www.example.com/path", want: "example.com"},
		{url: "https://a.b.example.co.uk/", want: "example.co.uk"},
		{url: "https://example.com:8443", want: "example.com"},
		{url: "http://127.0.0.1/", wantErr: true},
		{url: "http://localhost/", wantErr: true},
		{url: "not a url", wantErr: true},
	}
	for _, test := range tests {
		got, err := DomainForURL(test.url)
		if test.wantErr {
			if err == nil {
				t.Errorf("DomainForURL(%q) expected error, got %q", test.url, got)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("DomainForURL(%q) = %q, %v; want %q", test.url, got, err, test.want)
		}
	}
}

func TestFetchRDAP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRDAP(w)
	}))
	defer server.Close()

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	info, err := fetchRDAP(context.Background(), server.URL+"/domain/", "example.com", noProxy(t), now)
	if err != nil {
		t.Fatalf("fetchRDAP: %v", err)
	}
	if info.Registrar != "Example Registrar" || len(info.Statuses) != 1 {
		t.Fatalf("unexpected info: %+v", info)
	}
	if !info.ExpiresAt.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expiration = %s", info.ExpiresAt)
	}
}

func TestFetchRDAPRetriesTransientStatus(t *testing.T) {
	fastRetries(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) < 3 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		writeRDAP(w)
	}))
	defer server.Close()

	info, err := fetchRDAP(context.Background(), server.URL+"/domain/", "example.com", noProxy(t), time.Now().UTC())
	if err != nil {
		t.Fatalf("fetchRDAP: %v", err)
	}
	if info.Registrar != "Example Registrar" {
		t.Fatalf("registrar = %q", info.Registrar)
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d, want 3", requests.Load())
	}
}

func TestFetchRDAPRetriesClosedConnection(t *testing.T) {
	fastRetries(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
				return
			}
			http.Error(w, "hijack failed", http.StatusInternalServerError)
			return
		}
		writeRDAP(w)
	}))
	defer server.Close()

	info, err := fetchRDAP(context.Background(), server.URL+"/domain/", "example.com", noProxy(t), time.Now().UTC())
	if err != nil {
		t.Fatalf("fetchRDAP: %v", err)
	}
	if info.Registrar != "Example Registrar" {
		t.Fatalf("registrar = %q", info.Registrar)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestFetchRDAPDoesNotRetryPermanentStatus(t *testing.T) {
	fastRetries(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	_, err := fetchRDAP(context.Background(), server.URL+"/domain/", "example.com", noProxy(t), time.Now().UTC())
	if err == nil {
		t.Fatal("expected error")
	}
	if isTransientError(err) {
		t.Fatalf("404 must not be transient: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestRefreshKeepsPreviousInfoOnTransientFailure(t *testing.T) {
	fastRetries(t)
	var authoritativeCalls, fallbackCalls atomic.Int32
	authoritative := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		authoritativeCalls.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer authoritative.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer fallback.Close()
	useBootstrapServices(t, map[string][]string{"com": {authoritative.URL}})
	useFallbackEndpoint(t, fallback.URL+"/domain/")

	ctx := context.Background()
	st := newDomainStore(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	previous := model.DomainInfo{
		Domain:    "example.com",
		Registrar: "Old Registrar",
		Statuses:  []string{"active"},
		ExpiresAt: now.Add(100 * 24 * time.Hour),
		FetchedAt: now.Add(-24 * time.Hour),
	}
	if err := st.UpsertDomainInfo(ctx, previous); err != nil {
		t.Fatalf("UpsertDomainInfo: %v", err)
	}

	monitors := []model.Monitor{{
		Enabled: true,
		Type:    model.MonitorTypeHTTP,
		HTTP:    &model.HTTPConfig{URL: "https://example.com", DomainCheck: true},
	}}
	retry, err := Refresh(ctx, st, monitors, nil, now)
	if err == nil {
		t.Fatal("expected refresh error")
	}
	if !retry {
		t.Fatal("transient failure should request a retry")
	}
	if authoritativeCalls.Load() != 3 || fallbackCalls.Load() != 3 {
		t.Fatalf("calls = %d authoritative, %d fallback; want 3 each", authoritativeCalls.Load(), fallbackCalls.Load())
	}
	info, err := st.DomainInfo(ctx, "example.com")
	if err != nil {
		t.Fatalf("DomainInfo: %v", err)
	}
	if info.Registrar != "Old Registrar" || len(info.Statuses) != 1 || !info.ExpiresAt.Equal(previous.ExpiresAt) {
		t.Fatalf("previous data not preserved: %+v", info)
	}
	if info.Error == "" {
		t.Fatal("expected error to be stored")
	}
	if !info.FetchedAt.Equal(now) {
		t.Fatalf("fetched at = %s, want %s", info.FetchedAt, now)
	}
}

func TestRefreshUsesAuthoritativeEndpoint(t *testing.T) {
	fastRetries(t)
	authoritative := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRDAP(w)
	}))
	defer authoritative.Close()
	var fallbackCalls atomic.Int32
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		http.Error(w, "fallback should not be used", http.StatusServiceUnavailable)
	}))
	defer fallback.Close()
	useBootstrapServices(t, map[string][]string{"com": {authoritative.URL}})
	useFallbackEndpoint(t, fallback.URL+"/domain/")

	ctx := context.Background()
	st := newDomainStore(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	monitors := []model.Monitor{{
		Enabled: true,
		Type:    model.MonitorTypeHTTP,
		HTTP:    &model.HTTPConfig{URL: "https://example.com", DomainCheck: true},
	}}
	retry, err := Refresh(ctx, st, monitors, nil, now)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if retry {
		t.Fatal("successful refresh must not request a retry")
	}
	if fallbackCalls.Load() != 0 {
		t.Fatalf("fallback calls = %d, want 0", fallbackCalls.Load())
	}
	info, err := st.DomainInfo(ctx, "example.com")
	if err != nil {
		t.Fatalf("DomainInfo: %v", err)
	}
	if info.Registrar != "Example Registrar" || info.Error != "" {
		t.Fatalf("unexpected domain info: %+v", info)
	}
}

func TestRDAPEndpointsUsesBootstrap(t *testing.T) {
	useBootstrapServices(t, map[string][]string{"com": {"https://rdap.example/v1/"}})
	useFallbackEndpoint(t, "https://fallback.example/domain/")

	endpoints := rdapEndpoints(context.Background(), http.DefaultClient, "example.com")
	want := []string{"https://rdap.example/v1/domain/", "https://fallback.example/domain/"}
	if !slices.Equal(endpoints, want) {
		t.Fatalf("endpoints = %v, want %v", endpoints, want)
	}

	endpoints = rdapEndpoints(context.Background(), http.DefaultClient, "example.cn")
	want = []string{"https://fallback.example/domain/"}
	if !slices.Equal(endpoints, want) {
		t.Fatalf("fallback endpoints = %v, want %v", endpoints, want)
	}
}

func TestRDAPEndpointsCachesBootstrap(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"services":[[["com"],["https://rdap.example/v1/"]]]}`))
	}))
	defer server.Close()
	useBootstrapURL(t, server.URL)
	useFallbackEndpoint(t, "https://fallback.example/domain/")

	endpoints := rdapEndpoints(context.Background(), server.Client(), "example.com")
	want := []string{"https://rdap.example/v1/domain/", "https://fallback.example/domain/"}
	if !slices.Equal(endpoints, want) {
		t.Fatalf("endpoints = %v, want %v", endpoints, want)
	}
	endpoints = rdapEndpoints(context.Background(), server.Client(), "example.com")
	if !slices.Equal(endpoints, want) {
		t.Fatalf("cached endpoints = %v, want %v", endpoints, want)
	}
	if requests.Load() != 1 {
		t.Fatalf("bootstrap requests = %d, want 1", requests.Load())
	}
}

func TestRDAPEndpointsFallsBackWhenBootstrapFails(t *testing.T) {
	fastRetries(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	useBootstrapURL(t, server.URL)
	useFallbackEndpoint(t, "https://fallback.example/domain/")

	endpoints := rdapEndpoints(context.Background(), server.Client(), "example.com")
	want := []string{"https://fallback.example/domain/"}
	if !slices.Equal(endpoints, want) {
		t.Fatalf("endpoints = %v, want %v", endpoints, want)
	}
	if requests.Load() != 3 {
		t.Fatalf("bootstrap requests = %d, want 3", requests.Load())
	}

	endpoints = rdapEndpoints(context.Background(), server.Client(), "example.com")
	if !slices.Equal(endpoints, want) {
		t.Fatalf("fallback endpoints = %v, want %v", endpoints, want)
	}
	if requests.Load() != 3 {
		t.Fatalf("bootstrap requests after throttle = %d, want 3", requests.Load())
	}
}

func TestFetchRDAPBootstrapKeepsHTTPSOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"services":[[["com","net"],["https://rdap.example/v1/","http://rdap.example/v1/"]],[["org"],["http://rdap.example/org/"]]]}`))
	}))
	defer server.Close()
	useBootstrapURL(t, server.URL)

	services, err := fetchRDAPBootstrap(context.Background(), server.Client())
	if err != nil {
		t.Fatalf("fetchRDAPBootstrap: %v", err)
	}
	if !slices.Equal(services["com"], []string{"https://rdap.example/v1/"}) || !slices.Equal(services["net"], []string{"https://rdap.example/v1/"}) {
		t.Fatalf("unexpected services: %v", services)
	}
	if _, exists := services["org"]; exists {
		t.Fatalf("http-only service should be ignored: %v", services)
	}
}

func TestParseRDAPRequiresExpiration(t *testing.T) {
	if _, err := parseRDAP([]byte(`{"status":[]}`)); err == nil {
		t.Fatal("expected missing expiration error")
	}
}
