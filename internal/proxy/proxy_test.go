package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestConfigValidate(t *testing.T) {
	valid := &Config{Type: model.ProxyHTTP, Address: "127.0.0.1:7890"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	invalid := []*Config{
		nil,
		{Type: "ftp", Address: "127.0.0.1:7890"},
		{Type: model.ProxyHTTP, Address: "127.0.0.1"},
		{Type: model.ProxySOCKS5, Address: ":7890"},
	}
	for _, cfg := range invalid {
		if cfg == nil {
			if err := cfg.Validate(); err != nil {
				t.Fatalf("nil config should be allowed: %v", err)
			}
			continue
		}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected invalid config: %+v", cfg)
		}
	}
}

func TestHTTPProxyTransport(t *testing.T) {
	var hit atomic.Bool
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit.Store(true)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxyServer.Close()

	cfg := &Config{
		Type:    model.ProxyHTTP,
		Address: strings.TrimPrefix(proxyServer.URL, "http://"),
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if err := ApplyHTTPTransport(cfg, transport); err != nil {
		t.Fatalf("ApplyHTTPTransport: %v", err)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get("http://does-not-exist.invalid/")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "proxied" {
		t.Fatalf("proxy response status=%d body=%q", response.StatusCode, body)
	}
	if !hit.Load() {
		t.Fatal("request did not reach the proxy")
	}
}

func TestDirectDialContext(t *testing.T) {
	dial, err := DialContext(nil)
	if err != nil {
		t.Fatalf("DialContext(nil): %v", err)
	}
	if dial == nil {
		t.Fatal("missing direct dialer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = dial(ctx, "tcp", "127.0.0.1:1")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestProxyDialErrorsAreClassified(t *testing.T) {
	cfg := &Config{Type: model.ProxyHTTP, Address: "127.0.0.1:1"}
	dial, err := DialContext(cfg)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := dial(ctx, "tcp", "example.com:443"); err == nil {
		t.Fatal("expected proxy dial error")
	} else if !errors.Is(err, ErrProxyFailure) {
		t.Fatalf("error is not a proxy failure: %v", err)
	}
}
