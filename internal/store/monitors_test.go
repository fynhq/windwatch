package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func testHTTPMonitor(url string) model.Monitor {
	return model.Monitor{
		Name:             "Example HTTP",
		Type:             model.MonitorTypeHTTP,
		HTTP:             &model.HTTPConfig{URL: url, Method: "GET", ExpectedStatus: "200-299", FollowRedirects: true},
		Interval:         60 * time.Second,
		Timeout:          10 * time.Second,
		FailThreshold:    2,
		RecoverThreshold: 1,
		NotifyEnabled:    true,
		Enabled:          true,
	}
}

func TestMonitorCRUD(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	id, err := st.CreateMonitor(ctx, testHTTPMonitor("https://example.com"), now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if id <= 0 {
		t.Fatalf("monitor id = %d", id)
	}

	monitor, err := st.MonitorByID(ctx, id)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if monitor.Name != "Example HTTP" || monitor.HTTP == nil || monitor.HTTP.URL != "https://example.com" {
		t.Fatalf("unexpected monitor: %+v", monitor)
	}
	if monitor.State.Status != model.StatusPending {
		t.Fatalf("initial status = %q", monitor.State.Status)
	}

	monitor.Name = "Renamed HTTP"
	monitor.Interval = 120 * time.Second
	if err := st.UpdateMonitor(ctx, monitor, now.Add(time.Minute)); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}
	updated, err := st.MonitorByID(ctx, id)
	if err != nil {
		t.Fatalf("MonitorByID after update: %v", err)
	}
	if updated.Name != "Renamed HTTP" || updated.Interval != 120*time.Second {
		t.Fatalf("update was not persisted: %+v", updated)
	}

	if err := st.SetMonitorEnabled(ctx, id, false); err != nil {
		t.Fatalf("SetMonitorEnabled: %v", err)
	}
	monitors, err := st.ListMonitors(ctx)
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(monitors) != 1 || monitors[0].Enabled {
		t.Fatalf("unexpected monitors: %+v", monitors)
	}

	if err := st.DeleteMonitor(ctx, id); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	if _, err := st.MonitorByID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted monitor error = %v, want ErrNotFound", err)
	}
	if err := st.DeleteMonitor(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error = %v, want ErrNotFound", err)
	}
}

func TestRecordCheckAndHistory(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	id, err := st.CreateMonitor(ctx, testHTTPMonitor("https://example.com"), now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	upState := model.MonitorState{
		Status:               model.StatusUp,
		StatusSince:          now,
		LastCheck:            now,
		ConsecutiveSuccesses: 1,
		UpdatedAt:            now,
	}
	if err := st.RecordCheck(ctx, model.Check{
		MonitorID:  id,
		CheckedAt:  now,
		Status:     model.CheckUp,
		Latency:    42 * time.Millisecond,
		HTTPStatus: 200,
		MetaJSON:   `{"final_url":"https://example.com"}`,
	}, upState, nil, false); err != nil {
		t.Fatalf("RecordCheck up: %v", err)
	}

	downAt := now.Add(time.Minute)
	downState := model.MonitorState{
		Status:              model.StatusDown,
		StatusSince:         downAt,
		LastCheck:           downAt,
		LastError:           "connection refused",
		ConsecutiveFailures: 2,
		UpdatedAt:           downAt,
	}
	if err := st.RecordCheck(ctx, model.Check{
		MonitorID:    id,
		CheckedAt:    downAt,
		Status:       model.CheckDown,
		Latency:      80 * time.Millisecond,
		ErrorKind:    model.ErrorKindConnectionRefused,
		ErrorMessage: "connection refused",
		MetaJSON:     "{}",
	}, downState, &model.Event{
		MonitorID: id,
		Type:      model.EventDown,
		Message:   "connection refused",
		StartedAt: downAt,
		MetaJSON:  "{}",
	}, false); err != nil {
		t.Fatalf("RecordCheck down: %v", err)
	}
	openStart, open, err := st.OpenDownEventStart(ctx, id)
	if err != nil || !open || !openStart.Equal(downAt) {
		t.Fatalf("OpenDownEventStart = %s, %v, %v", openStart, open, err)
	}

	recoveryAt := now.Add(2 * time.Minute)
	recoveredState := model.MonitorState{
		Status:               model.StatusUp,
		StatusSince:          recoveryAt,
		LastCheck:            recoveryAt,
		ConsecutiveSuccesses: 1,
		UpdatedAt:            recoveryAt,
	}
	if err := st.RecordCheck(ctx, model.Check{
		MonitorID:  id,
		CheckedAt:  recoveryAt,
		Status:     model.CheckUp,
		Latency:    20 * time.Millisecond,
		HTTPStatus: 200,
		MetaJSON:   "{}",
	}, recoveredState, &model.Event{
		MonitorID: id,
		Type:      model.EventUp,
		StartedAt: recoveryAt,
		MetaJSON:  "{}",
	}, true); err != nil {
		t.Fatalf("RecordCheck recovery: %v", err)
	}
	if _, open, err := st.OpenDownEventStart(ctx, id); err != nil || open {
		t.Fatalf("open down event after recovery = %v, %v", open, err)
	}

	stored, err := st.MonitorByID(ctx, id)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if stored.State.Status != model.StatusUp || stored.State.LastError != "" {
		t.Fatalf("unexpected state: %+v", stored.State)
	}
	if stored.State.ConsecutiveSuccesses != 1 {
		t.Fatalf("consecutive successes = %d", stored.State.ConsecutiveSuccesses)
	}

	checks, err := st.RecentChecks(ctx, id, 10)
	if err != nil {
		t.Fatalf("RecentChecks: %v", err)
	}
	if len(checks) != 3 || checks[0].Status != model.CheckUp || checks[1].Status != model.CheckDown {
		t.Fatalf("unexpected checks: %+v", checks)
	}
	byMonitor, err := st.RecentChecksByMonitor(ctx, []int64{id}, 2)
	if err != nil {
		t.Fatalf("RecentChecksByMonitor: %v", err)
	}
	recent := byMonitor[id]
	if len(recent) != 2 || recent[0].Status != model.CheckUp || recent[1].Status != model.CheckDown {
		t.Fatalf("unexpected recent checks: %+v", recent)
	}

	stats, err := st.UptimeStats(ctx, id, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("UptimeStats: %v", err)
	}
	if stats.Total != 3 || stats.Up != 2 || stats.Down != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if stats.AvgLatency <= 0 {
		t.Fatalf("avg latency = %s", stats.AvgLatency)
	}

	events, err := st.RecentEvents(ctx, id, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Type != model.EventUp || events[1].Type != model.EventDown || events[1].EndedAt == nil {
		t.Fatalf("unexpected events: %+v", events)
	}

	buckets, err := st.HourlyBuckets(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("HourlyBuckets: %v", err)
	}
	if len(buckets[id]) == 0 {
		t.Fatalf("missing buckets: %+v", buckets)
	}
}

func TestMonitorDeleteCascadesHistory(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	id, err := st.CreateMonitor(ctx, testHTTPMonitor("https://example.com"), now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if err := st.RecordCheck(ctx, model.Check{
		MonitorID: id,
		CheckedAt: now,
		Status:    model.CheckUp,
		Latency:   time.Millisecond,
		MetaJSON:  "{}",
	}, model.MonitorState{
		Status:      model.StatusUp,
		StatusSince: now,
		LastCheck:   now,
		UpdatedAt:   now,
	}, nil, false); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if err := st.DeleteMonitor(ctx, id); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	checks, err := st.RecentChecks(ctx, id, 10)
	if err != nil {
		t.Fatalf("RecentChecks: %v", err)
	}
	if len(checks) != 0 {
		t.Fatalf("checks were not cascaded: %+v", checks)
	}
	events, err := st.RecentEvents(ctx, id, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events were not cascaded: %+v", events)
	}
}
