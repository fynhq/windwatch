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

func TestRollupStartDoesNotClampToRawRetention(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	last := now.Add(-10 * 24 * time.Hour)
	if got := rollupStart(now, last, true); !got.Equal(last.Add(-30 * time.Minute)) {
		t.Fatalf("rollup start with old last bucket = %s, want %s", got, last.Add(-30*time.Minute))
	}
	wantFresh := now.Add(-rawRetention).Truncate(30 * time.Minute)
	if got := rollupStart(now, time.Time{}, false); !got.Equal(wantFresh) {
		t.Fatalf("rollup start without history = %s, want %s", got, wantFresh)
	}
}

func TestResumeDownMonitorDoesNotReopenOutage(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "resume.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	monitor := model.Monitor{
		Name: "Push", Type: model.MonitorTypePush,
		Interval: time.Minute, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1, Enabled: true,
	}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if err := st.RecordCheck(t.Context(), model.Check{
		MonitorID: id, CheckedAt: now, Status: model.CheckDown,
		ErrorKind: model.ErrorKindNetwork, ErrorMessage: "down", MetaJSON: "{}",
	}, model.MonitorState{
		Status: model.StatusDown, StatusSince: now, LastCheck: now,
		LastError: "down", UpdatedAt: now,
	}, &model.Event{MonitorID: id, Type: model.EventDown, StartedAt: now, MetaJSON: "{}"}, false); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	scheduler := New(st, checker.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		scheduler.Wait()
	})

	if err := scheduler.SetMonitorEnabled(ctx, id, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := scheduler.SetMonitorEnabled(ctx, id, true); err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumed, err := st.MonitorByID(ctx, id)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if resumed.State.Status != model.StatusPending {
		t.Fatalf("status after resume = %q, want pending", resumed.State.Status)
	}
	if _, open, err := st.OpenDownEventStart(ctx, id); err != nil {
		t.Fatalf("OpenDownEventStart: %v", err)
	} else if open {
		t.Fatal("resume reopened a DOWN event")
	}
}

func TestWarningIsSuppressedWhileDown(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	now := time.Now().UTC().Truncate(time.Second)
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	monitor.NotifyEnabled = true
	monitor.FailThreshold = 2
	monitor.RecoverThreshold = 2
	monitor.NotificationChannelIDs = []int64{channelID}
	monitor.HTTP.CertCheck = true
	monitor.HTTP.CertWarnDays = 30
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	for i := 0; i < 2; i++ {
		s.apply(t.Context(), outcome{
			monitorID: id,
			result:    model.CheckResult{Success: false, ErrorMessage: "down"},
			checkedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}
	cert := &model.CertificateInfo{Fingerprint: "warning-fp", Issuer: "CA", NotAfter: now.Add(2 * time.Hour)}
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true, Certificate: cert},
		checkedAt: now.Add(3 * time.Minute),
	})
	for _, notification := range sink.snapshot() {
		if notification.Kind == model.EventWarnCert {
			t.Fatalf("warning was emitted while state was still DOWN: %+v", notification)
		}
	}
}

func TestPauseCounterResetPreventsPrePauseFailureFromStartingOutage(t *testing.T) {
	s, st, _, channelID := newNotificationTestScheduler(t)
	now := time.Now().UTC().Truncate(time.Second)
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	monitor.NotifyEnabled = true
	monitor.FailThreshold = 2
	monitor.RecoverThreshold = 1
	monitor.NotificationChannelIDs = []int64{channelID}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: false, ErrorMessage: "pre-pause"},
		checkedAt: now,
	})
	if err := st.ResetMonitorCounters(t.Context(), id, now.Add(time.Hour)); err != nil {
		t.Fatalf("ResetMonitorCounters: %v", err)
	}
	resumed := now.Add(2 * time.Hour)
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: false, ErrorMessage: "post-resume"},
		checkedAt: resumed,
	})
	events, err := st.RecentEvents(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("a single failure after reset must not reach fail_threshold: %+v", events)
	}
}

func TestWarningContextSurvivesSubthresholdFailure(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	now := time.Now().UTC().Truncate(time.Second)
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	monitor.NotifyEnabled = true
	monitor.FailThreshold = 2
	monitor.RecoverThreshold = 1
	monitor.HTTP.CertCheck = true
	monitor.HTTP.CertWarnDays = 30
	monitor.NotificationChannelIDs = []int64{channelID}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	cert := &model.CertificateInfo{Fingerprint: "stable-fp", Issuer: "CA", NotAfter: now.Add(2 * time.Hour)}
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true, Certificate: cert},
		checkedAt: now,
	})
	if got := len(sink.snapshot()); got != 1 {
		t.Fatalf("initial warning notifications = %d, want 1", got)
	}
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: false, ErrorMessage: "transient"},
		checkedAt: now.Add(time.Minute),
	})
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true, Certificate: cert},
		checkedAt: now.Add(2 * time.Minute),
	})
	if got := len(sink.snapshot()); got != 1 {
		t.Fatalf("subthreshold failure caused duplicate warning: %d notifications", got)
	}
}

func TestWarningKeyIncludesEpisodeStart(t *testing.T) {
	monitor := model.Monitor{
		ID:   7,
		HTTP: &model.HTTPConfig{URL: "https://example.com", CertCheck: true, CertWarnDays: 30},
	}
	s := &Scheduler{}
	cert := &model.CertificateInfo{Fingerprint: "fp", NotAfter: time.Now().Add(24 * time.Hour)}
	first := s.warningNotification(monitor, model.CheckResult{Certificate: cert}, auxWarning{cert: true}, model.MonitorState{Status: model.StatusWarning, StatusSince: time.UnixMilli(1000)}, time.Now())
	second := s.warningNotification(monitor, model.CheckResult{Certificate: cert}, auxWarning{cert: true}, model.MonitorState{Status: model.StatusWarning, StatusSince: time.UnixMilli(2000)}, time.Now())
	if first.Key == second.Key {
		t.Fatalf("warning keys for different episodes must differ: %q", first.Key)
	}
}

func TestWarningIsNotRepeatedAfterRestart(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	st, err := store.Open(filepath.Join(t.TempDir(), "restart.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	channelID, err := st.CreateNotificationChannel(t.Context(), model.NotificationChannel{
		Name: "Test webhook", Type: model.NotificationChannelWebhook, Enabled: true,
		WebhookURL: "https://example.com/hook",
	}, now)
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	monitor.NotifyEnabled = true
	monitor.HTTP.CertCheck = true
	monitor.HTTP.CertWarnDays = 30
	monitor.NotificationChannelIDs = []int64{channelID}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := New(st, checker.NewRegistry(), logger)
	firstSink := &fakeNotificationSink{enabled: true}
	first.SetNotifier(firstSink)
	cert := &model.CertificateInfo{Fingerprint: "restart-fp", Issuer: "CA", NotAfter: now.Add(2 * time.Hour)}
	first.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true, Certificate: cert},
		checkedAt: now,
	})
	if got := len(firstSink.snapshot()); got != 1 {
		t.Fatalf("initial warning notifications = %d, want 1", got)
	}
	before, err := st.MonitorByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}

	// A restarted process builds a fresh scheduler over the same database.
	second := New(st, checker.NewRegistry(), logger)
	secondSink := &fakeNotificationSink{enabled: true}
	second.SetNotifier(secondSink)
	second.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true, Certificate: cert},
		checkedAt: now.Add(time.Minute),
	})
	if got := len(secondSink.snapshot()); got != 0 {
		t.Fatalf("restart re-sent %d warning notifications, want 0", got)
	}
	after, err := st.MonitorByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorByID after restart: %v", err)
	}
	if !after.State.StatusSince.Equal(before.State.StatusSince) {
		t.Fatalf("warning status_since changed across restart: %v -> %v", before.State.StatusSince, after.State.StatusSince)
	}
}
