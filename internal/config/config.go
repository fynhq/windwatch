package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	defaultAddr    = ":8080"
	defaultDataDir = "./data"
	defaultLog     = "info"
)

type Config struct {
	Addr           string
	DataDir        string
	LogLevel       slog.Level
	LogDir         string
	TrustedProxies []netip.Prefix
}

func ParseServe(args []string, getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:    envOrDefault(getenv, "WINDWATCH_ADDR", defaultAddr),
		DataDir: envOrDefault(getenv, "WINDWATCH_DATA_DIR", defaultDataDir),
	}
	logLevel := envOrDefault(getenv, "WINDWATCH_LOG_LEVEL", defaultLog)
	logDir := envOrDefault(getenv, "WINDWATCH_LOG_DIR", "")
	trustedProxies := envOrDefault(getenv, "WINDWATCH_TRUSTED_PROXIES", "")

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "persistent data directory")
	fs.StringVar(&logLevel, "log-level", logLevel, "log level: debug, info, warn, error")
	fs.StringVar(&logDir, "log-dir", logDir, "directory for daily log files (defaults to <data-dir>/logs)")
	fs.StringVar(&trustedProxies, "trusted-proxies", trustedProxies, "comma-separated trusted proxy IPs or CIDRs")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	level, err := parseLogLevel(logLevel)
	if err != nil {
		return Config{}, err
	}
	cfg.LogLevel = level

	if strings.TrimSpace(logDir) == "" {
		logDir = filepath.Join(cfg.DataDir, "logs")
	}
	cfg.LogDir = logDir

	prefixes, err := parseTrustedProxies(trustedProxies)
	if err != nil {
		return Config{}, err
	}
	cfg.TrustedProxies = prefixes

	if strings.TrimSpace(cfg.Addr) == "" {
		return Config{}, errors.New("listen address must not be empty")
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		return Config{}, errors.New("data directory must not be empty")
	}
	return cfg, nil
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q", value)
	}
}

func envOrDefault(getenv func(string) string, key, fallback string) string {
	if getenv == nil {
		return fallback
	}
	if value := getenv(key); value != "" {
		return value
	}
	return fallback
}

func parseTrustedProxies(value string) ([]netip.Prefix, error) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	if len(fields) == 0 {
		return nil, nil
	}
	prefixes := make([]netip.Prefix, 0, len(fields))
	for _, field := range fields {
		if strings.Contains(field, "/") {
			prefix, err := netip.ParsePrefix(field)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", field, err)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(field)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy IP %q: %w", field, err)
		}
		addr = addr.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}
