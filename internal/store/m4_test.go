package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func newStoreTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func TestMonitorReminderPolicyRoundTrip(t *testing.T) {
	st := newStoreTest(t)
	now := time.Now().UTC()
	monitorID, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "Reminders", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: time.Second, FailThreshold: 1, RecoverThreshold: 1,
		Reminders: model.NotificationPolicy{
			WarningReminderCount:    3,
			WarningReminderInterval: 2 * time.Hour,
			FailureReminderCount:    2,
			FailureReminderInterval: 15 * time.Minute,
		},
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	monitor, err := st.MonitorByID(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if monitor.Reminders.WarningReminderCount != 3 || monitor.Reminders.WarningReminderInterval != 2*time.Hour ||
		monitor.Reminders.FailureReminderCount != 2 || monitor.Reminders.FailureReminderInterval != 15*time.Minute {
		t.Fatalf("stored reminders = %+v", monitor.Reminders)
	}

	monitor.Reminders.FailureReminderCount = 4
	monitor.Reminders.FailureReminderInterval = 45 * time.Minute
	if err := st.UpdateMonitor(t.Context(), monitor, now); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}
	monitor, err = st.MonitorByID(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("MonitorByID after update: %v", err)
	}
	if monitor.Reminders.FailureReminderCount != 4 || monitor.Reminders.FailureReminderInterval != 45*time.Minute {
		t.Fatalf("updated reminders = %+v", monitor.Reminders)
	}
}

func TestReminderStateRoundTrip(t *testing.T) {
	st := newStoreTest(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	id, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "Reminder", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: time.Second, FailThreshold: 1, RecoverThreshold: 1,
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	state := model.NotificationReminder{
		MonitorID: id, Kind: "down", StatusSince: now, ContextKey: "",
		SentCount: 1, NextAt: now.Add(30 * time.Minute),
	}
	if err := st.SaveReminderState(t.Context(), state, now); err != nil {
		t.Fatalf("SaveReminderState: %v", err)
	}
	loaded, found, err := st.ReminderState(t.Context(), id, "down")
	if err != nil || !found {
		t.Fatalf("ReminderState found=%v err=%v", found, err)
	}
	if loaded.SentCount != 1 || loaded.NextAt.UnixMilli() != state.NextAt.UnixMilli() {
		t.Fatalf("loaded state = %+v", loaded)
	}
	if err := st.DeleteReminderStates(t.Context(), id); err != nil {
		t.Fatalf("DeleteReminderStates: %v", err)
	}
	if _, found, err := st.ReminderState(t.Context(), id, "down"); err != nil || found {
		t.Fatalf("state still present found=%v err=%v", found, err)
	}
}

func TestHalfHourlyRollupAggregation(t *testing.T) {
	st := newStoreTest(t)
	now := time.UnixMilli(time.Now().UTC().UnixMilli()).UTC()
	start := now.Truncate(30 * time.Minute).Add(-2 * time.Hour)
	id, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "Rollup", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: time.Second, FailThreshold: 1, RecoverThreshold: 1,
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	checks := []struct {
		at     time.Time
		status string
	}{
		{start.Add(5 * time.Minute), model.CheckUp},
		{start.Add(10 * time.Minute), model.CheckUp},
		{start.Add(65 * time.Minute), model.CheckDown},
		{start.Add(70 * time.Minute), model.CheckWarning},
	}
	for _, check := range checks {
		state := model.MonitorState{
			Status: model.StatusUp, StatusSince: check.at, LastCheck: check.at, UpdatedAt: check.at,
		}
		if err := st.RecordCheck(t.Context(), model.Check{
			MonitorID: id, CheckedAt: check.at, Status: check.status,
			Latency: 20 * time.Millisecond, MetaJSON: "{}",
		}, state, nil, false); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
	}

	if err := st.AggregateHalfHourlyRollups(t.Context(), start, start.Add(3*time.Hour), now); err != nil {
		t.Fatalf("AggregateHalfHourlyRollups: %v", err)
	}
	stats, err := st.RollupStats(t.Context(), id, start, start.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("RollupStats: %v", err)
	}
	if stats.Total != 4 || stats.Up != 2 || stats.Down != 1 || stats.Warning != 1 {
		t.Fatalf("rollup stats = %+v", stats)
	}
	if stats.AvgLatency != 20*time.Millisecond {
		t.Fatalf("avg latency = %s", stats.AvgLatency)
	}

	last, ok, err := st.LastRollupStart(t.Context())
	if err != nil || !ok || last.UnixMilli() != start.Add(time.Hour).UnixMilli() {
		t.Fatalf("last rollup = %s ok=%v err=%v", last, ok, err)
	}

	if err := st.RecordCheck(t.Context(), model.Check{
		MonitorID: id, CheckedAt: start.Add(20 * time.Minute), Status: model.CheckDown,
		Latency: 50 * time.Millisecond, MetaJSON: "{}",
	}, model.MonitorState{
		Status: model.StatusDown, StatusSince: start.Add(20 * time.Minute),
		LastCheck: start.Add(20 * time.Minute), UpdatedAt: start.Add(20 * time.Minute),
	}, nil, false); err != nil {
		t.Fatalf("RecordCheck late: %v", err)
	}
	if err := st.AggregateHalfHourlyRollups(t.Context(), start, start.Add(3*time.Hour), now); err != nil {
		t.Fatalf("re-aggregate: %v", err)
	}
	stats, err = st.RollupStats(t.Context(), id, start, start.Add(3*time.Hour))
	if err != nil || stats.Total != 5 || stats.Down != 2 {
		t.Fatalf("re-aggregated stats = %+v err=%v", stats, err)
	}

	if err := st.DeleteRollupsBefore(t.Context(), start.Add(2*time.Hour)); err != nil {
		t.Fatalf("DeleteRollupsBefore: %v", err)
	}
	stats, err = st.RollupStats(t.Context(), id, start, start.Add(3*time.Hour))
	if err != nil || stats.Total != 0 {
		t.Fatalf("stats after rollup cleanup = %+v err=%v", stats, err)
	}
}

func TestDailyBucketsForMonitors(t *testing.T) {
	st := newStoreTest(t)
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	localDay := time.Date(2026, 1, 1, 0, 0, 0, 0, location)
	dayStartUTC := localDay.UTC()
	first := createStoreHTTPMonitor(t, st, "First daily")
	second := createStoreHTTPMonitor(t, st, "Second daily")
	for _, item := range []struct {
		id int64
		at time.Time
		up bool
	}{
		{first, dayStartUTC.Add(10 * time.Minute), true},
		{first, dayStartUTC.Add(40 * time.Minute), false},
		{second, dayStartUTC.Add(20 * time.Minute), true},
	} {
		status := model.CheckDown
		if item.up {
			status = model.CheckUp
		}
		if err := st.RecordCheck(t.Context(), model.Check{
			MonitorID: item.id, CheckedAt: item.at, Status: status,
			Latency: 20 * time.Millisecond, MetaJSON: "{}",
		}, model.MonitorState{
			Status: model.StatusUp, StatusSince: item.at, LastCheck: item.at, UpdatedAt: item.at,
		}, nil, false); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
	}
	if err := st.AggregateHalfHourlyRollups(t.Context(), dayStartUTC.Add(-time.Hour), dayStartUTC.Add(24*time.Hour), dayStartUTC); err != nil {
		t.Fatalf("AggregateHalfHourlyRollups: %v", err)
	}

	daily, err := st.DailyBucketsForMonitors(t.Context(), []int64{first, second}, localDay, localDay.AddDate(0, 0, 1), location)
	if err != nil {
		t.Fatalf("DailyBucketsForMonitors: %v", err)
	}
	if len(daily[first]) != 1 || daily[first][0].Total != 2 || daily[first][0].Up != 1 || daily[first][0].Down != 1 {
		t.Fatalf("first daily buckets = %+v", daily[first])
	}
	if len(daily[second]) != 1 || daily[second][0].Total != 1 || daily[second][0].Up != 1 {
		t.Fatalf("second daily buckets = %+v", daily[second])
	}
	if daily[first][0].AvgLatency != 20*time.Millisecond {
		t.Fatalf("first avg latency = %s", daily[first][0].AvgLatency)
	}

	empty, err := st.DailyBucketsForMonitors(t.Context(), nil, localDay, localDay.AddDate(0, 0, 1), location)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty daily buckets = %+v err=%v", empty, err)
	}
}

func TestHalfHourlyRollupTimezoneAggregation(t *testing.T) {
	st := newStoreTest(t)
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	localDay := time.Date(2026, 1, 1, 0, 0, 0, 0, location)
	dayStartUTC := localDay.UTC()
	id, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "Half-hourly", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: time.Second, FailThreshold: 1, RecoverThreshold: 1,
	}, dayStartUTC)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	checks := []time.Time{
		dayStartUTC.Add(-10 * time.Minute),
		dayStartUTC.Add(10 * time.Minute),
		dayStartUTC.Add(23*time.Hour + 40*time.Minute),
	}
	for _, checkedAt := range checks {
		if err := st.RecordCheck(t.Context(), model.Check{
			MonitorID: id, CheckedAt: checkedAt, Status: model.CheckUp,
			Latency: 20 * time.Millisecond, MetaJSON: "{}",
		}, model.MonitorState{
			Status: model.StatusUp, StatusSince: checkedAt, LastCheck: checkedAt, UpdatedAt: checkedAt,
		}, nil, false); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
	}
	if err := st.AggregateHalfHourlyRollups(t.Context(), dayStartUTC.Add(-time.Hour), dayStartUTC.Add(24*time.Hour), dayStartUTC); err != nil {
		t.Fatalf("AggregateHalfHourlyRollups: %v", err)
	}

	daily, err := st.DailyBucketsForTimezone(t.Context(), id, localDay, localDay.AddDate(0, 0, 1), location)
	if err != nil {
		t.Fatalf("DailyBucketsForTimezone: %v", err)
	}
	if len(daily) != 1 || daily[0].Total != 2 || daily[0].Up != 2 {
		t.Fatalf("Shanghai daily buckets = %+v", daily)
	}
	if daily[0].Start.UnixMilli() != dayStartUTC.UnixMilli() {
		t.Fatalf("daily start = %s, want %s", daily[0].Start, dayStartUTC)
	}

	utcDaily, err := st.DailyBucketsForTimezone(t.Context(), id, localDay, localDay.AddDate(0, 0, 1), time.UTC)
	if err != nil {
		t.Fatalf("DailyBucketsForTimezone UTC: %v", err)
	}
	if len(utcDaily) != 2 {
		t.Fatalf("UTC daily buckets = %+v, want 2", utcDaily)
	}
}
