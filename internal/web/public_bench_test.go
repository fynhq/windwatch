package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

func benchmarkPublicServer(b *testing.B, monitors int) (*Server, []int64) {
	b.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(b.TempDir(), "windwatch.db"))
	if err != nil {
		b.Fatalf("store.Open: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		b.Fatalf("store.Migrate: %v", err)
	}
	catalogs, err := i18n.Load()
	if err != nil {
		b.Fatalf("i18n.Load: %v", err)
	}
	cipher, err := secret.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		b.Fatalf("secret.NewCipher: %v", err)
	}
	srv, err := New(st, catalogs, &fakeScheduler{st: st}, &fakeNotifier{}, cipher, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
	if err != nil {
		b.Fatalf("web.New: %v", err)
	}

	now := time.Now().UTC()
	ids := make([]int64, 0, monitors)
	for i := 0; i < monitors; i++ {
		id, err := st.CreateMonitor(ctx, model.Monitor{
			Name:             fmt.Sprintf("public-%d", i),
			Type:             model.MonitorTypeTCP,
			TCP:              &model.TCPConfig{Host: "127.0.0.1", Port: 9000 + i},
			Interval:         60 * time.Second,
			Timeout:          10 * time.Second,
			FailThreshold:    2,
			RecoverThreshold: 1,
			NotifyEnabled:    true,
			Enabled:          true,
		}, now)
		if err != nil {
			b.Fatalf("CreateMonitor: %v", err)
		}
		ids = append(ids, id)
		for check := 0; check < 60; check++ {
			at := now.Add(-time.Duration(check) * time.Minute)
			state := model.MonitorState{Status: model.StatusUp, StatusSince: at, LastCheck: at, UpdatedAt: at}
			if err := st.RecordCheck(ctx, model.Check{
				MonitorID: id,
				CheckedAt: at,
				Status:    model.CheckUp,
				Latency:   20 * time.Millisecond,
				MetaJSON:  "{}",
			}, state, nil, false); err != nil {
				b.Fatalf("RecordCheck: %v", err)
			}
		}
	}
	return srv, ids
}

func BenchmarkPublicSnapshot(b *testing.B) {
	srv, ids := benchmarkPublicServer(b, 10)
	ctx := context.Background()
	now := time.Now().UTC()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// A new revision forces the expensive aggregate rebuild instead of a
		// cache hit.
		if _, err := srv.loadPublicSnapshot(ctx, ids, uint64(i+1), now, time.UTC); err != nil {
			b.Fatalf("loadPublicSnapshot: %v", err)
		}
	}
}
