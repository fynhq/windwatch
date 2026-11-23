package store

import (
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestUptimeStatsCombinedSevenDayBoundary(t *testing.T) {
	st, err := Open(t.TempDir() + "/boundary.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 11, 7, 12, 15, 0, 0, time.UTC)
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)
	monitor := model.Monitor{
		Name: "boundary", Type: model.MonitorTypeTCP,
		TCP:      &model.TCPConfig{Host: "127.0.0.1", Port: 80},
		Interval: time.Minute, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1, Enabled: true,
	}
	id, err := st.CreateMonitor(t.Context(), monitor, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	state := model.MonitorState{
		Status: model.StatusUp, StatusSince: now.Add(-30 * 24 * time.Hour),
		LastCheck: now, UpdatedAt: now,
	}
	record := func(at time.Time, latency time.Duration) {
		if err := st.RecordCheck(t.Context(), model.Check{
			MonitorID: id, CheckedAt: at, Status: model.CheckUp, Latency: latency, MetaJSON: "{}",
		}, state, nil, false); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
	}
	record(sevenDaysAgo.Add(-10*time.Minute), time.Millisecond)
	record(sevenDaysAgo.Add(5*time.Minute), 2*time.Millisecond)
	record(now.Add(-5*time.Minute), 0)

	if err := st.AggregateHalfHourlyRollups(t.Context(), now.Add(-30*24*time.Hour), now, now); err != nil {
		t.Fatalf("AggregateHalfHourlyRollups: %v", err)
	}

	stats30Map, err := st.UptimeStatsForMonitorsCombined(t.Context(), []int64{id}, now.Add(-30*24*time.Hour), now)
	if err != nil {
		t.Fatalf("30d combined stats: %v", err)
	}
	stats30 := stats30Map[id]
	if stats30.Total != 3 {
		t.Fatalf("30d total = %d, want 3 (rollup and trailing raw must not overlap)", stats30.Total)
	}
	if stats30.AvgLatency != time.Millisecond {
		t.Fatalf("30d avg latency = %s, want 1ms from exact sum merge", stats30.AvgLatency)
	}

	stats7Map, err := st.UptimeStatsForMonitorsCombined(t.Context(), []int64{id}, now.Add(-7*24*time.Hour), now)
	if err != nil {
		t.Fatalf("7d combined stats: %v", err)
	}
	stats7 := stats7Map[id]
	if stats7.Total != 3 {
		t.Fatalf("7d total = %d, want 3 (window start floors to the containing bucket, including the check 10 minutes before the exact start)", stats7.Total)
	}
	if stats7.AvgLatency != time.Millisecond {
		t.Fatalf("7d avg latency = %s, want 1ms", stats7.AvgLatency)
	}
}

func TestUptimeStatsCombinedTrailingBoundaryNoOverlap(t *testing.T) {
	st, err := Open(t.TempDir() + "/trailing.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, 11, 7, 12, 15, 0, 0, time.UTC)
	since := time.Date(2026, 11, 7, 11, 0, 0, 0, time.UTC)
	id, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "trailing", Type: model.MonitorTypeTCP,
		TCP:      &model.TCPConfig{Host: "127.0.0.1", Port: 80},
		Interval: time.Minute, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1, Enabled: true,
	}, since)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	state := model.MonitorState{Status: model.StatusUp, StatusSince: since, LastCheck: now, UpdatedAt: now}
	record := func(at time.Time, latency time.Duration) {
		if err := st.RecordCheck(t.Context(), model.Check{
			MonitorID: id, CheckedAt: at, Status: model.CheckUp, Latency: latency, MetaJSON: "{}",
		}, state, nil, false); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
	}
	record(time.Date(2026, 11, 7, 11, 40, 0, 0, time.UTC), 3*time.Millisecond)
	record(time.Date(2026, 11, 7, 12, 5, 0, 0, time.UTC), time.Millisecond)
	record(time.Date(2026, 11, 7, 12, 10, 0, 0, time.UTC), 2*time.Millisecond)
	if err := st.AggregateHalfHourlyRollups(t.Context(), since, now, now); err != nil {
		t.Fatalf("AggregateHalfHourlyRollups: %v", err)
	}

	statsMap, err := st.UptimeStatsForMonitorsCombined(t.Context(), []int64{id}, since, now)
	if err != nil {
		t.Fatalf("combined stats: %v", err)
	}
	stats := statsMap[id]
	if stats.Total != 3 {
		t.Fatalf("total = %d, want 3 (aggregated bucket and unaggregated tail must not double-count)", stats.Total)
	}
	if stats.AvgLatency != 2*time.Millisecond {
		t.Fatalf("avg latency = %s, want 2ms (merge must sum raw latencies, not weight truncated averages)", stats.AvgLatency)
	}
}
