package scheduler

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/checker"
	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

func TestStaleResultDoesNotAdvanceChangedConfig(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	now := time.Now().UTC().Truncate(time.Second)
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	monitor.NotificationChannelIDs = []int64{channelID}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	current, err := st.MonitorConfigByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorConfigByID: %v", err)
	}
	current.Name = "Changed"
	if err := st.UpdateMonitor(t.Context(), current, now.Add(time.Minute)); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}

	s.apply(t.Context(), outcome{
		monitorID:  id,
		result:     model.CheckResult{Success: false, ErrorMessage: "stale"},
		checkedAt:  now,
		generation: 0,
		revision:   current.ConfigRevision,
		versioned:  true,
	})
	checks, err := st.RecentCheckSummaries(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentCheckSummaries: %v", err)
	}
	if len(checks) != 0 {
		t.Fatalf("stale result persisted checks: %+v", checks)
	}
	if notifications := sink.snapshot(); len(notifications) != 0 {
		t.Fatalf("stale result emitted notifications: %+v", notifications)
	}
}

func TestFailureStartedAtSurvivesRawCheckCleanup(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	now := time.Now().UTC().Truncate(time.Second)
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	monitor.FailThreshold = 2
	monitor.NotificationChannelIDs = []int64{channelID}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: false, ErrorMessage: "first"},
		checkedAt: now,
	})
	if err := st.DeleteChecksBefore(t.Context(), now.Add(time.Second)); err != nil {
		t.Fatalf("DeleteChecksBefore: %v", err)
	}
	confirmedAt := now.Add(time.Minute)
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: false, ErrorMessage: "second"},
		checkedAt: confirmedAt,
	})
	events, err := st.RecentEvents(t.Context(), id, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	if !events[0].StartedAt.Equal(now) {
		t.Fatalf("event start = %s, want persisted first failure %s", events[0].StartedAt, now)
	}
	current, err := st.MonitorConfigByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorConfigByID: %v", err)
	}
	if !current.State.FailureStartedAt.Equal(now) {
		t.Fatalf("failure start = %s, want %s", current.State.FailureStartedAt, now)
	}
	if len(sink.snapshot()) != 1 {
		t.Fatalf("DOWN notification count = %d", len(sink.snapshot()))
	}

	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true},
		checkedAt: confirmedAt.Add(time.Minute),
	})
	current, err = st.MonitorConfigByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorConfigByID after recovery: %v", err)
	}
	if !current.State.FailureStartedAt.IsZero() {
		t.Fatalf("failure start was not cleared: %s", current.State.FailureStartedAt)
	}
}

func TestShutdownDrainsResultsQueuedByWorkers(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "shutdown.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(st, checker.NewRegistry(), logger)
	s.initialDelay = func(time.Duration) time.Duration { return time.Hour }
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	started := make(chan struct{})
	s.workerWG.Add(1)
	go func() {
		defer s.workerWG.Done()
		close(started)
		<-ctx.Done()
		s.sendOutcome(ctx, outcome{
			monitorID: id,
			result:    model.CheckResult{Success: true, MetaJSON: "{}"},
			checkedAt: now,
		})
	}()
	<-started
	cancel()
	s.Wait()

	stats, err := st.UptimeStats(t.Context(), id, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("UptimeStats: %v", err)
	}
	if stats.Total != 1 {
		t.Fatalf("drained checks = %d, want 1", stats.Total)
	}
}
