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

func newPushExpiryFixture(t *testing.T, now time.Time, failThreshold, recoverThreshold int) (*Scheduler, *store.Store, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "push-expiry.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	id, err := st.CreateMonitorWithPushToken(t.Context(), model.Monitor{
		Name: "Push expiry", Type: model.MonitorTypePush,
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailThreshold: failThreshold, RecoverThreshold: recoverThreshold, Enabled: true,
	}, now, "hash", "enc")
	if err != nil {
		t.Fatalf("CreateMonitorWithPushToken: %v", err)
	}
	s := New(st, checker.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return now }
	return s, st, id
}

func evaluatePush(t *testing.T, s *Scheduler, st *store.Store, id int64) model.CheckResult {
	t.Helper()
	monitor, err := st.MonitorConfigByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorConfigByID: %v", err)
	}
	s.enqueuePushOutcomeVersioned(t.Context(), monitor, false, 0, true)
	select {
	case job := <-s.results:
		s.apply(context.Background(), job)
		return job.result
	case <-time.After(time.Second):
		t.Fatal("push outcome was not enqueued")
		return model.CheckResult{}
	}
}

func TestPushHeartbeatExpiryEntersDown(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	s, st, id := newPushExpiryFixture(t, now, 2, 1)
	if err := st.RecordHeartbeat(t.Context(), id, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	first := evaluatePush(t, s, st, id)
	if first.Success || first.ErrorKind != model.ErrorKindHeartbeatTimeout {
		t.Fatalf("expired heartbeat result = %+v", first)
	}
	current, err := st.MonitorByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if current.State.Status != model.StatusPending {
		t.Fatalf("status after one expired evaluation = %q, want pending below threshold", current.State.Status)
	}

	evaluatePush(t, s, st, id)
	current, err = st.MonitorByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if current.State.Status != model.StatusDown || current.State.ConsecutiveFailures != 2 {
		t.Fatalf("state after threshold = %+v, want down with 2 failures", current.State)
	}
	events, err := st.RecentEvents(t.Context(), id, 10)
	if err != nil || len(events) != 1 || events[0].Type != model.EventDown {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestPushHeartbeatBoundaryStaysUp(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	s, st, id := newPushExpiryFixture(t, now, 1, 1)
	if err := st.RecordHeartbeat(t.Context(), id, now.Add(-(time.Minute + 5*time.Second))); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	result := evaluatePush(t, s, st, id)
	if !result.Success {
		t.Fatalf("heartbeat at interval+timeout boundary = %+v, want success", result)
	}
	current, err := st.MonitorByID(t.Context(), id)
	if err != nil || current.State.Status != model.StatusUp {
		t.Fatalf("state=%+v err=%v", current.State, err)
	}
}

func TestPushWithoutHeartbeatStaysPending(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	s, st, id := newPushExpiryFixture(t, now, 1, 1)
	monitor, err := st.MonitorConfigByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorConfigByID: %v", err)
	}
	s.enqueuePushOutcomeVersioned(t.Context(), monitor, false, 0, true)
	select {
	case job := <-s.results:
		t.Fatalf("evaluation without heartbeat enqueued %+v", job)
	default:
	}
	current, err := st.MonitorByID(t.Context(), id)
	if err != nil || current.State.Status != model.StatusPending {
		t.Fatalf("state=%+v err=%v", current.State, err)
	}
}

func TestPushRecoversAfterFreshHeartbeat(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	s, st, id := newPushExpiryFixture(t, now, 1, 1)
	if err := st.RecordHeartbeat(t.Context(), id, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	evaluatePush(t, s, st, id)
	current, err := st.MonitorByID(t.Context(), id)
	if err != nil || current.State.Status != model.StatusDown {
		t.Fatalf("state=%+v err=%v", current.State, err)
	}
	if err := st.RecordHeartbeat(t.Context(), id, now); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	s.now = func() time.Time { return now.Add(time.Minute) }
	result := evaluatePush(t, s, st, id)
	if !result.Success {
		t.Fatalf("fresh heartbeat result = %+v", result)
	}
	current, err = st.MonitorByID(t.Context(), id)
	if err != nil || current.State.Status != model.StatusUp {
		t.Fatalf("state=%+v err=%v", current.State, err)
	}
	events, err := st.RecentEvents(t.Context(), id, 10)
	if err != nil || len(events) != 2 || events[0].Type != model.EventUp {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}
