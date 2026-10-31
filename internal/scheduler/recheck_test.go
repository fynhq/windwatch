package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/checker"
	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

func TestShouldRecheck(t *testing.T) {
	s := &Scheduler{}
	failure := model.CheckResult{Success: false, ErrorKind: model.ErrorKindTimeout}

	tests := []struct {
		name    string
		monitor model.Monitor
		result  model.CheckResult
		want    bool
	}{
		{
			name:    "success",
			monitor: model.Monitor{Type: model.MonitorTypeHTTP, FailThreshold: 1},
			result:  model.CheckResult{Success: true},
			want:    false,
		},
		{
			name:    "push",
			monitor: model.Monitor{Type: model.MonitorTypePush, FailThreshold: 1},
			result:  failure,
			want:    false,
		},
		{
			name: "already down",
			monitor: model.Monitor{
				Type: model.MonitorTypeHTTP, FailThreshold: 1,
				State: model.MonitorState{Status: model.StatusDown},
			},
			result: failure,
			want:   false,
		},
		{
			name: "below threshold",
			monitor: model.Monitor{
				Type: model.MonitorTypeHTTP, FailThreshold: 2,
				State: model.MonitorState{Status: model.StatusUp, ConsecutiveFailures: 0},
			},
			result: failure,
			want:   false,
		},
		{
			name: "crosses threshold",
			monitor: model.Monitor{
				Type: model.MonitorTypeHTTP, FailThreshold: 2,
				State: model.MonitorState{Status: model.StatusUp, ConsecutiveFailures: 1},
			},
			result: failure,
			want:   true,
		},
		{
			name: "threshold one",
			monitor: model.Monitor{
				Type: model.MonitorTypeHTTP, FailThreshold: 1,
				State: model.MonitorState{Status: model.StatusUp},
			},
			result: failure,
			want:   true,
		},
		{
			name: "invalid config",
			monitor: model.Monitor{
				Type: model.MonitorTypeHTTP, FailThreshold: 1,
				State: model.MonitorState{Status: model.StatusUp},
			},
			result: model.CheckResult{Success: false, ErrorKind: model.ErrorKindInvalidConfig},
			want:   false,
		},
		{
			name: "internal error",
			monitor: model.Monitor{
				Type: model.MonitorTypeHTTP, FailThreshold: 1,
				State: model.MonitorState{Status: model.StatusUp},
			},
			result: model.CheckResult{Success: false, ErrorKind: model.ErrorKindInternal},
			want:   false,
		},
		{
			name: "unknown error kind",
			monitor: model.Monitor{
				Type: model.MonitorTypeHTTP, FailThreshold: 1,
				State: model.MonitorState{Status: model.StatusUp},
			},
			result: model.CheckResult{Success: false},
			want:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := s.shouldRecheck(test.monitor, test.result); got != test.want {
				t.Fatalf("shouldRecheck = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRecheckFailureHonorsCancellation(t *testing.T) {
	s := &Scheduler{recheckDelay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := s.recheckFailure(ctx, model.Monitor{Timeout: time.Second}, nil, model.CheckResult{Success: false}); ok {
		t.Fatal("recheckFailure returned true for a canceled context")
	}
}

func TestApplyUsesFirstFailureForDownEvent(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "first-failure.db"))
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
	checkedAt := now.Add(10 * time.Second)
	s.apply(t.Context(), outcome{
		monitorID:        id,
		result:           model.CheckResult{Success: false, ErrorKind: model.ErrorKindTimeout, ErrorMessage: "timeout"},
		checkedAt:        checkedAt,
		failureStartedAt: now,
	})

	events, err := st.RecentEvents(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != model.EventDown || !events[0].StartedAt.Equal(now) {
		t.Fatalf("events = %+v, want DOWN started at %s", events, now)
	}

	current, err := st.MonitorByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if current.State.Status != model.StatusDown || !current.State.StatusSince.Equal(checkedAt) {
		t.Fatalf("state = %+v, want DOWN since %s", current.State, checkedAt)
	}

	checks, err := st.RecentChecks(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentChecks: %v", err)
	}
	if len(checks) != 1 || !checks[0].CheckedAt.Equal(checkedAt) {
		t.Fatalf("checks = %+v, want one check at %s", checks, checkedAt)
	}
}

func TestSchedulerRecheckClearsTransientFailure(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	now := time.Now().UTC()
	s.recheckDelay = 10 * time.Millisecond
	s.recheckTimeout = 200 * time.Millisecond
	s.now = func() time.Time { return now }

	runCtx, cancel := context.WithCancel(context.Background())
	if err := s.Start(runCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		s.Wait()
	})

	monitor := schedulerTestMonitor(server.URL)
	monitor.Enabled = true
	monitor.NotifyEnabled = true
	monitor.NotificationChannelIDs = []int64{channelID}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	checkNowEventually(t, s, id)
	waitFor(t, 3*time.Second, func() bool {
		current, err := st.MonitorByID(t.Context(), id)
		return err == nil && current.State.Status == model.StatusUp
	})

	if got := requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	checks, err := st.RecentChecks(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentChecks: %v", err)
	}
	if len(checks) != 1 || checks[0].Status != model.CheckUp {
		t.Fatalf("checks = %+v, want one successful check", checks)
	}
	events, err := st.RecentEvents(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
	if notifications := sink.snapshot(); len(notifications) != 0 {
		t.Fatalf("notifications = %+v, want none", notifications)
	}
}

func TestSchedulerRecheckConfirmsFailure(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	now := time.Now().UTC()
	s.recheckDelay = 10 * time.Millisecond
	s.recheckTimeout = 200 * time.Millisecond
	s.now = func() time.Time { return now }

	runCtx, cancel := context.WithCancel(context.Background())
	if err := s.Start(runCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		s.Wait()
	})

	monitor := schedulerTestMonitor(server.URL)
	monitor.Enabled = true
	monitor.NotifyEnabled = true
	monitor.NotificationChannelIDs = []int64{channelID}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	checkNowEventually(t, s, id)
	waitFor(t, 3*time.Second, func() bool {
		current, err := st.MonitorByID(t.Context(), id)
		return err == nil && current.State.Status == model.StatusDown
	})
	waitFor(t, 3*time.Second, func() bool {
		return len(sink.snapshot()) == 1
	})

	if got := requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	checks, err := st.RecentChecks(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentChecks: %v", err)
	}
	if len(checks) != 1 || checks[0].Status != model.CheckDown {
		t.Fatalf("checks = %+v, want one failed check", checks)
	}
	events, err := st.RecentEvents(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != model.EventDown {
		t.Fatalf("events = %+v, want one DOWN event", events)
	}
	notifications := sink.snapshot()
	if len(notifications) != 1 || notifications[0].Kind != model.EventDown {
		t.Fatalf("notifications = %+v, want one DOWN notification", notifications)
	}
}
