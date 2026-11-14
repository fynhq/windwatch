package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/config"
)

func testConfig(dataDir string) config.Config {
	return config.Config{
		Addr:     "127.0.0.1:0",
		DataDir:  dataDir,
		LogLevel: slog.LevelError,
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunServesAndShutsDown(t *testing.T) {
	dataDir := t.TempDir()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	addr := reservation.Addr().String()
	_ = reservation.Close()

	cfg := testConfig(dataDir)
	cfg.Addr = addr
	application, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()

	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("health endpoint did not become ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	for _, name := range []string{"windwatch.db", "secret.key"} {
		if _, err := os.Stat(filepath.Join(dataDir, name)); err != nil {
			t.Fatalf("%s was not created: %v", name, err)
		}
	}
	info, err := os.Stat(filepath.Join(dataDir, "secret.key"))
	if err != nil {
		t.Fatalf("stat secret.key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("secret.key mode = %o, want 600", perm)
	}
}

func TestRunReportsListenFailure(t *testing.T) {
	application, err := New(testConfig(t.TempDir()), testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	application.cfg.Addr = "127.0.0.1:-1"
	err = application.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("Run error = %v, want listen failure", err)
	}
}

func TestNewRejectsUnusableDataDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := New(testConfig(file), testLogger()); err == nil {
		t.Fatal("New accepted a file as the data directory")
	}
}
