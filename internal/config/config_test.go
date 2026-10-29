package config

import (
	"net/netip"
	"path/filepath"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func TestParseServeDefaults(t *testing.T) {
	cfg, err := ParseServe(nil, env(nil))
	if err != nil {
		t.Fatalf("ParseServe: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.DataDir != "./data" {
		t.Fatalf("DataDir = %q, want ./data", cfg.DataDir)
	}
	if want := filepath.Join("./data", "logs"); cfg.LogDir != want {
		t.Fatalf("LogDir = %q, want %q", cfg.LogDir, want)
	}
}

func TestParseServeLogDir(t *testing.T) {
	cfg, err := ParseServe(nil, env(map[string]string{"WINDWATCH_LOG_DIR": "/var/log/windwatch"}))
	if err != nil {
		t.Fatalf("ParseServe env: %v", err)
	}
	if cfg.LogDir != "/var/log/windwatch" {
		t.Fatalf("LogDir = %q, want /var/log/windwatch", cfg.LogDir)
	}

	cfg, err = ParseServe([]string{"--data-dir", "/srv/windwatch"}, env(nil))
	if err != nil {
		t.Fatalf("ParseServe data-dir flag: %v", err)
	}
	if want := filepath.Join("/srv/windwatch", "logs"); cfg.LogDir != want {
		t.Fatalf("LogDir = %q, want %q", cfg.LogDir, want)
	}

	cfg, err = ParseServe([]string{"--log-dir", "/tmp/ww-logs"}, env(nil))
	if err != nil {
		t.Fatalf("ParseServe log-dir flag: %v", err)
	}
	if cfg.LogDir != "/tmp/ww-logs" {
		t.Fatalf("LogDir = %q, want /tmp/ww-logs", cfg.LogDir)
	}
}

func TestParseServeEnvironmentAndFlags(t *testing.T) {
	getenv := env(map[string]string{
		"WINDWATCH_ADDR":      ":9000",
		"WINDWATCH_DATA_DIR":  "/tmp/from-env",
		"WINDWATCH_LOG_LEVEL": "debug",
	})
	cfg, err := ParseServe([]string{"--addr", ":9100", "--log-level", "warn"}, getenv)
	if err != nil {
		t.Fatalf("ParseServe: %v", err)
	}
	if cfg.Addr != ":9100" {
		t.Fatalf("Addr = %q, want :9100", cfg.Addr)
	}
	if cfg.DataDir != "/tmp/from-env" {
		t.Fatalf("DataDir = %q, want /tmp/from-env", cfg.DataDir)
	}
	if cfg.LogLevel.String() != "WARN" {
		t.Fatalf("LogLevel = %s, want WARN", cfg.LogLevel)
	}
}

func TestParseServeRejectsInvalidInput(t *testing.T) {
	if _, err := ParseServe([]string{"--log-level", "verbose"}, env(nil)); err == nil {
		t.Fatal("expected invalid log level error")
	}
	if _, err := ParseServe([]string{"--addr", ""}, env(nil)); err == nil {
		t.Fatal("expected empty address error")
	}
	if _, err := ParseServe([]string{"--data-dir", " "}, env(nil)); err == nil {
		t.Fatal("expected empty data dir error")
	}
	if _, err := ParseServe([]string{"--trusted-proxies", "not-an-ip"}, env(nil)); err == nil {
		t.Fatal("expected invalid trusted proxy error")
	}
}

func TestParseServeTrustedProxies(t *testing.T) {
	cfg, err := ParseServe(nil, env(map[string]string{
		"WINDWATCH_TRUSTED_PROXIES": "10.0.0.0/8, 192.0.2.5 2001:db8::/32",
	}))
	if err != nil {
		t.Fatalf("ParseServe: %v", err)
	}
	if len(cfg.TrustedProxies) != 3 {
		t.Fatalf("TrustedProxies len = %d, want 3", len(cfg.TrustedProxies))
	}
	for _, value := range []string{"10.1.2.3", "192.0.2.5", "2001:db8::1"} {
		addr := netip.MustParseAddr(value)
		found := false
		for _, prefix := range cfg.TrustedProxies {
			if prefix.Contains(addr) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("trusted proxies do not contain %s", value)
		}
	}

	cfg, err = ParseServe([]string{"--trusted-proxies", "203.0.113.7"}, env(nil))
	if err != nil {
		t.Fatalf("ParseServe flag: %v", err)
	}
	if len(cfg.TrustedProxies) != 1 || !cfg.TrustedProxies[0].Contains(netip.MustParseAddr("203.0.113.7")) {
		t.Fatalf("flag trusted proxies = %v", cfg.TrustedProxies)
	}
}
