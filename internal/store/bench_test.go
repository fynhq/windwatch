package store

import (
	"context"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func benchmarkStore(b *testing.B) *Store {
	b.Helper()
	st, err := Open(":memory:")
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		b.Fatalf("Migrate: %v", err)
	}
	return st
}

func benchmarkMonitor(b *testing.B, st *Store, now time.Time) int64 {
	b.Helper()
	id, err := st.CreateMonitor(context.Background(), model.Monitor{
		Name:             "benchmark",
		Type:             model.MonitorTypeTCP,
		TCP:              &model.TCPConfig{Host: "127.0.0.1", Port: 9000},
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
	return id
}

func BenchmarkRecordCheck(b *testing.B) {
	ctx := context.Background()
	st := benchmarkStore(b)
	now := time.Now().UTC()
	monitorID := benchmarkMonitor(b, st, now)
	state := model.MonitorState{
		Status:      model.StatusUp,
		StatusSince: now,
		LastCheck:   now,
		UpdatedAt:   now,
	}
	check := model.Check{
		MonitorID: monitorID,
		CheckedAt: now,
		Status:    model.CheckUp,
		Latency:   25 * time.Millisecond,
		MetaJSON:  "{}",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		check.CheckedAt = at
		state.LastCheck = at
		state.UpdatedAt = at
		if err := st.RecordCheck(ctx, check, state, nil, false); err != nil {
			b.Fatalf("RecordCheck: %v", err)
		}
	}
}

func BenchmarkUptimeStats(b *testing.B) {
	ctx := context.Background()
	st := benchmarkStore(b)
	now := time.Now().UTC()
	monitorID := benchmarkMonitor(b, st, now)

	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatalf("begin seed: %v", err)
	}
	for i := 0; i < 5000; i++ {
		at := now.Add(-time.Duration(i) * time.Minute)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO checks (monitor_id, checked_at_ms, status, latency_ms, meta_json)
			VALUES (?, ?, 'up', 20, '{}')
		`, monitorID, at.UnixMilli()); err != nil {
			_ = tx.Rollback()
			b.Fatalf("seed check: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit seed: %v", err)
	}

	since := now.Add(-24 * time.Hour)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.UptimeStats(ctx, monitorID, since); err != nil {
			b.Fatalf("UptimeStats: %v", err)
		}
	}
}
