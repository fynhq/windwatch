package store

import (
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestUptimeStatsForMonitorCombinedSevenDayBoundary(t *testing.T) {
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
	record := func(at time.Time) {
		if err := st.RecordCheck(t.Context(), model.Check{
			MonitorID: id, CheckedAt: at, Status: model.CheckUp, Latency: time.Millisecond, MetaJSON: "{}",
		}, state, nil, false); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
	}
	// Both checks are in the 12:00-12:30 bucket seven days ago. The raw 7d
	// window starts at 12:15, so only the second check is inside it.
	record(sevenDaysAgo.Add(-10 * time.Minute)) // 12:05
	record(sevenDaysAgo.Add(5 * time.Minute))   // 12:20

	if err := st.AggregateHalfHourlyRollups(t.Context(), now.Add(-30*24*time.Hour), now, now); err != nil {
		t.Fatalf("AggregateHalfHourlyRollups: %v", err)
	}

	stats30, err := st.UptimeStatsForMonitorCombined(t.Context(), id, now.Add(-30*24*time.Hour), now)
	if err != nil {
		t.Fatalf("30d combined stats: %v", err)
	}
	if stats30.Total != 2 {
		t.Fatalf("30d total = %d, want 2 (boundary bucket must not be counted twice)", stats30.Total)
	}

	stats7, err := st.UptimeStatsForMonitorCombined(t.Context(), id, now.Add(-7*24*time.Hour), now)
	if err != nil {
		t.Fatalf("7d combined stats: %v", err)
	}
	if stats7.Total != 1 {
		t.Fatalf("7d total = %d, want 1 (only the check after the exact 7d start)", stats7.Total)
	}
}
