package checker

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func monitorWithHTTP(url string, configure func(*model.HTTPConfig)) model.Monitor {
	cfg := &model.HTTPConfig{
		URL:             url,
		Method:          http.MethodGet,
		ExpectedStatus:  "200-299",
		FollowRedirects: true,
	}
	if configure != nil {
		configure(cfg)
	}
	return model.Monitor{
		Name:             "HTTP monitor",
		Type:             model.MonitorTypeHTTP,
		HTTP:             cfg,
		Interval:         60 * time.Second,
		Timeout:          10 * time.Second,
		FailThreshold:    2,
		RecoverThreshold: 1,
		Enabled:          true,
		NotifyEnabled:    true,
	}
}

func TestHTTPCheckerSuccessAndStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("WindWatch is here"))
	}))
	defer server.Close()

	registry := NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := registry.CheckWithProxy(ctx, monitorWithHTTP(server.URL, nil), nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.Success || result.HTTPStatus != http.StatusOK {
		t.Fatalf("unexpected success result: %+v", result)
	}
	if result.MetaJSON == "" || !strings.Contains(result.MetaJSON, "final_url") {
		t.Fatalf("missing meta: %q", result.MetaJSON)
	}

	result, err = registry.CheckWithProxy(ctx, monitorWithHTTP(server.URL+"/missing", nil), nil)
	if err != nil {
		t.Fatalf("Check missing: %v", err)
	}
	if result.Success || result.ErrorKind != model.ErrorKindInvalidStatus || result.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("unexpected status result: %+v", result)
	}
}

func TestHTTPCheckerKeyword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer server.Close()
	registry := NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := registry.CheckWithProxy(ctx, monitorWithHTTP(server.URL, func(cfg *model.HTTPConfig) {
		cfg.Keyword = "hello"
	}), nil)
	if err != nil || !result.Success {
		t.Fatalf("keyword match result=%+v err=%v", result, err)
	}

	result, err = registry.CheckWithProxy(ctx, monitorWithHTTP(server.URL, func(cfg *model.HTTPConfig) {
		cfg.Keyword = "missing"
	}), nil)
	if err != nil || result.Success || result.ErrorKind != model.ErrorKindKeywordMissing {
		t.Fatalf("keyword mismatch result=%+v err=%v", result, err)
	}
}

func TestHTTPCheckerRedirectPolicy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	registry := NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := registry.CheckWithProxy(ctx, monitorWithHTTP(redirect.URL, nil), nil)
	if err != nil || !result.Success {
		t.Fatalf("follow redirect result=%+v err=%v", result, err)
	}

	result, err = registry.CheckWithProxy(ctx, monitorWithHTTP(redirect.URL, func(cfg *model.HTTPConfig) {
		cfg.FollowRedirects = false
	}), nil)
	if err != nil || result.Success || result.ErrorKind != model.ErrorKindInvalidStatus {
		t.Fatalf("no-follow result=%+v err=%v", result, err)
	}
}

func TestHTTPCheckerTimeoutAndTLS(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	registry := NewRegistry()
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	result, err := registry.CheckWithProxy(timeoutCtx, monitorWithHTTP(slow.URL, nil), nil)
	if err != nil {
		t.Fatalf("timeout check: %v", err)
	}
	if result.Success || result.ErrorKind != model.ErrorKindTimeout {
		t.Fatalf("unexpected timeout result: %+v", result)
	}

	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer tlsServer.Close()
	tlsCtx, cancelTLS := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelTLS()
	result, err = registry.CheckWithProxy(tlsCtx, monitorWithHTTP(tlsServer.URL, nil), nil)
	if err != nil {
		t.Fatalf("TLS check: %v", err)
	}
	if result.Success || result.ErrorKind != model.ErrorKindTLS {
		t.Fatalf("unexpected TLS result: %+v", result)
	}

	result, err = registry.CheckWithProxy(tlsCtx, monitorWithHTTP(tlsServer.URL, func(cfg *model.HTTPConfig) {
		cfg.TLSSkipVerify = true
	}), nil)
	if err != nil {
		t.Fatalf("TLS skip verify check: %v", err)
	}
	if !result.Success || result.Certificate == nil || result.Certificate.NotAfter.IsZero() {
		t.Fatalf("expected certificate capture with skip verify: %+v", result)
	}
}

func TestHTTPCheckerHead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %s, want HEAD", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	registry := NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := registry.CheckWithProxy(ctx, monitorWithHTTP(server.URL, func(cfg *model.HTTPConfig) {
		cfg.Method = http.MethodHead
	}), nil)
	if err != nil || !result.Success {
		t.Fatalf("HEAD result=%+v err=%v", result, err)
	}
}

func TestTCPChecker(t *testing.T) {
	registry := NewRegistry()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	monitor := model.Monitor{
		Name:             "TCP monitor",
		Type:             model.MonitorTypeTCP,
		TCP:              &model.TCPConfig{Host: "127.0.0.1", Port: port},
		Interval:         60 * time.Second,
		Timeout:          10 * time.Second,
		FailThreshold:    2,
		RecoverThreshold: 1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := registry.CheckWithProxy(ctx, monitor, nil)
	if err != nil {
		t.Fatalf("TCP check: %v", err)
	}
	if !result.Success {
		t.Fatalf("TCP check failed: %+v", result)
	}
	listener.Close()

	deadline := time.Now().Add(time.Second)
	for {
		result, err = registry.CheckWithProxy(ctx, monitor, nil)
		if err != nil {
			t.Fatalf("closed TCP check: %v", err)
		}
		if !result.Success {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("closed port still accepted connections: %+v", result)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRegistryRejectsInvalidConfig(t *testing.T) {
	registry := NewRegistry()
	monitor := monitorWithHTTP("ftp://example.com", nil)
	result, err := registry.CheckWithProxy(context.Background(), monitor, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Success || result.ErrorKind != model.ErrorKindInvalidConfig {
		t.Fatalf("unexpected invalid config result: %+v", result)
	}
}

func TestTCPAddressUsesPort(t *testing.T) {
	monitor := model.Monitor{
		Name:     "IPv6",
		Type:     model.MonitorTypeTCP,
		TCP:      &model.TCPConfig{Host: "::1", Port: 8080},
		Interval: 60 * time.Second,
		Timeout:  10 * time.Second,
	}
	if got := monitor.Target(); got != net.JoinHostPort("::1", strconv.Itoa(8080)) {
		t.Fatalf("Target() = %q", got)
	}
}

func TestHTTPCheckerKeywordBodyLimit(t *testing.T) {
	largePrefix := "needle" + strings.Repeat("x", maxKeywordBodyBytes+100)
	largeSuffix := strings.Repeat("x", maxKeywordBodyBytes+100) + "needle"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/suffix" {
			_, _ = w.Write([]byte(largeSuffix))
			return
		}
		_, _ = w.Write([]byte(largePrefix))
	}))
	defer server.Close()

	registry := NewRegistry()
	result, err := registry.CheckWithProxy(t.Context(), monitorWithHTTP(server.URL, func(cfg *model.HTTPConfig) {
		cfg.Keyword = "needle"
	}), nil)
	if err != nil || !result.Success {
		t.Fatalf("keyword in first MiB: result=%+v err=%v", result, err)
	}

	result, err = registry.CheckWithProxy(t.Context(), monitorWithHTTP(server.URL+"/suffix", func(cfg *model.HTTPConfig) {
		cfg.Keyword = "needle"
	}), nil)
	if err != nil || result.Success || result.ErrorKind != model.ErrorKindKeywordMissing ||
		!strings.Contains(result.ErrorMessage, "first 1 MiB") {
		t.Fatalf("keyword beyond limit: result=%+v err=%v", result, err)
	}
}
