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
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

func schedulerTestMonitor(url string) model.Monitor {
	return model.Monitor{
		Name:             "Scheduler test",
		Type:             model.MonitorTypeHTTP,
		HTTP:             &model.HTTPConfig{URL: url, Method: http.MethodGet, ExpectedStatus: "200-299", FollowRedirects: true},
		Interval:         60 * time.Second,
		Timeout:          2 * time.Second,
		FailThreshold:    1,
		RecoverThreshold: 1,
		NotifyEnabled:    true,
		Enabled:          false,
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func checkNowEventually(t *testing.T, s *Scheduler, monitorID int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		err := s.CheckNow(monitorID)
		if err == nil {
			return
		}
		if err != ErrCheckRunning {
			t.Fatalf("CheckNow: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("CheckNow was busy until timeout")
}

func TestAdvanceStateMachine(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	monitor := model.Monitor{ID: 7, FailThreshold: 2, RecoverThreshold: 2}
	state := model.MonitorState{Status: model.StatusPending, StatusSince: at}

	transition := advance(monitor, state, model.CheckResult{Success: true}, at, false, "")
	if transition.state.Status != model.StatusUp || transition.event != nil || transition.closeDown {
		t.Fatalf("pending success state=%+v event=%+v close=%v", transition.state, transition.event, transition.closeDown)
	}

	state = transition.state
	transition = advance(monitor, state, model.CheckResult{Success: false, ErrorMessage: "first"}, at.Add(time.Minute), false, "")
	if transition.state.Status != model.StatusUp || transition.state.ConsecutiveFailures != 1 || transition.event != nil || transition.closeDown {
		t.Fatalf("first failure state=%+v event=%+v close=%v", transition.state, transition.event, transition.closeDown)
	}

	state = transition.state
	transition = advance(monitor, state, model.CheckResult{Success: false, ErrorMessage: "second"}, at.Add(2*time.Minute), false, "")
	if transition.state.Status != model.StatusDown || transition.state.ConsecutiveFailures != 2 || transition.event == nil || transition.event.Type != model.EventDown || transition.closeDown {
		t.Fatalf("second failure state=%+v event=%+v close=%v", transition.state, transition.event, transition.closeDown)
	}

	state = transition.state
	transition = advance(monitor, state, model.CheckResult{Success: true}, at.Add(3*time.Minute), false, "")
	if transition.state.Status != model.StatusDown || transition.state.ConsecutiveSuccesses != 1 || transition.event != nil || transition.closeDown {
		t.Fatalf("first recovery state=%+v event=%+v close=%v", transition.state, transition.event, transition.closeDown)
	}

	state = transition.state
	transition = advance(monitor, state, model.CheckResult{Success: true}, at.Add(4*time.Minute), false, "")
	if transition.state.Status != model.StatusUp || transition.state.ConsecutiveSuccesses != 2 || transition.event == nil || transition.event.Type != model.EventUp || !transition.closeDown {
		t.Fatalf("second recovery state=%+v event=%+v close=%v", transition.state, transition.event, transition.closeDown)
	}
}

func TestAdvanceWarningTransitions(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	monitor := model.Monitor{ID: 9, FailThreshold: 2, RecoverThreshold: 1}
	state := model.MonitorState{Status: model.StatusPending, StatusSince: at}
	success := model.CheckResult{Success: true}

	transition := advance(monitor, state, success, at, true, "cert:abc")
	if transition.state.Status != model.StatusWarning || transition.checkStatus != model.CheckWarning ||
		!transition.warningChanged || transition.warningContext == nil || *transition.warningContext != "cert:abc" {
		t.Fatalf("enter warning = %+v", transition)
	}
	if transition.state.StatusSince != at {
		t.Fatalf("warning status since = %s, want %s", transition.state.StatusSince, at)
	}

	transition = advance(monitor, transition.state, success, at.Add(time.Minute), true, "cert:abc")
	if transition.state.Status != model.StatusWarning || transition.warningChanged ||
		transition.state.StatusSince != at || transition.warningContext == nil {
		t.Fatalf("same warning episode = %+v", transition)
	}

	transition = advance(monitor, transition.state, model.CheckResult{Success: false, ErrorMessage: "boom"}, at.Add(2*time.Minute), false, "")
	if transition.state.Status != model.StatusWarning || transition.checkStatus != model.CheckDown ||
		transition.warningChanged || transition.warningContext != nil {
		t.Fatalf("warning with failed check = %+v", transition)
	}

	transition = advance(monitor, transition.state, success, at.Add(3*time.Minute), true, "cert:abc")
	if transition.state.Status != model.StatusWarning || transition.warningChanged || transition.state.StatusSince != at {
		t.Fatalf("warning after subthreshold failure = %+v", transition)
	}

	transition = advance(monitor, transition.state, success, at.Add(4*time.Minute), false, "")
	if transition.state.Status != model.StatusUp || transition.warningContext == nil || *transition.warningContext != "" {
		t.Fatalf("warning cleared = %+v", transition)
	}
}

func TestAdvanceRecoveryWithWarning(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	monitor := model.Monitor{ID: 10, FailThreshold: 1, RecoverThreshold: 1}
	state := model.MonitorState{Status: model.StatusDown, StatusSince: at.Add(-time.Minute), LastError: "boom"}
	transition := advance(monitor, state, model.CheckResult{Success: true}, at, true, "cert:abc")
	if transition.state.Status != model.StatusWarning || transition.checkStatus != model.CheckWarning ||
		transition.event == nil || transition.event.Type != model.EventUp || !transition.closeDown ||
		!transition.warningChanged || transition.state.StatusSince != at {
		t.Fatalf("recovery with warning = %+v", transition)
	}
}

func TestAuxiliaryWarnings(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "warnings.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	monitor := model.Monitor{
		Name:             "HTTPS",
		Type:             model.MonitorTypeHTTP,
		HTTP:             &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299", CertCheck: true, CertWarnDays: 30},
		Interval:         60 * time.Second,
		Timeout:          10 * time.Second,
		FailThreshold:    1,
		RecoverThreshold: 1,
		Enabled:          true,
	}
	if _, err := st.CreateMonitor(t.Context(), monitor, time.Now().UTC()); err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(st, checker.NewRegistry(), logger)
	result := model.CheckResult{
		Success:     true,
		Certificate: &model.CertificateInfo{NotAfter: time.Now().Add(10 * 24 * time.Hour)},
	}
	if !s.auxiliaryWarning(monitor, result).active {
		t.Fatal("expected certificate warning")
	}
	result.Certificate = &model.CertificateInfo{NotAfter: time.Now().Add(90 * 24 * time.Hour)}
	if s.auxiliaryWarning(monitor, result).active {
		t.Fatal("did not expect a warning")
	}
}

func TestPushOutcomeAndProxyResolution(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "push.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().UTC()
	push := model.Monitor{
		Name:             "Push",
		Type:             model.MonitorTypePush,
		Interval:         time.Minute,
		Timeout:          10 * time.Second,
		FailThreshold:    1,
		RecoverThreshold: 1,
		Enabled:          true,
	}
	id, err := st.CreateMonitorWithPushToken(t.Context(), push, now, "hash", "enc")
	if err != nil {
		t.Fatalf("CreateMonitorWithPushToken: %v", err)
	}
	push.ID = id
	if err := st.RecordHeartbeat(t.Context(), id, now); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(st, checker.NewRegistry(), logger)
	s.now = func() time.Time { return now.Add(5 * time.Second) }
	pushConfig, err := st.MonitorConfigByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorConfigByID: %v", err)
	}
	s.enqueuePushOutcomeVersioned(t.Context(), pushConfig, false, 0, true)
	select {
	case outcome := <-s.results:
		if !outcome.result.Success {
			t.Fatalf("fresh heartbeat result: %+v", outcome.result)
		}
	case <-time.After(time.Second):
		t.Fatal("push outcome was not enqueued")
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, err := secret.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	s.SetCipher(cipher)
	encrypted, err := cipher.Encrypt("password")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	proxyID, err := st.CreateProxy(t.Context(), model.Proxy{
		Name: "proxy", Type: model.ProxyHTTP, Address: "127.0.0.1:7890", Username: "", PasswordEnc: encrypted,
	}, now)
	if err != nil {
		t.Fatalf("CreateProxy: %v", err)
	}
	config, err := s.resolveProxy(t.Context(), proxyID)
	if err != nil || config.Password != "password" {
		t.Fatalf("resolveProxy=%+v err=%v", config, err)
	}
}

func TestSchedulerManualCheckPersistsResults(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "windwatch.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var healthy atomic.Bool
	healthy.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	now := time.Now().UTC()
	monitor := schedulerTestMonitor(server.URL)
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(st, checker.NewRegistry(), logger)
	s.initialDelay = func(time.Duration) time.Duration { return 0 }
	s.nextDelay = func(d time.Duration) time.Duration { return d }
	s.recheckDelay = 0

	runCtx, cancel := context.WithCancel(context.Background())
	if err := s.Start(runCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		s.Wait()
	})

	checkNowEventually(t, s, id)
	waitFor(t, 3*time.Second, func() bool {
		current, err := st.MonitorByID(t.Context(), id)
		return err == nil && current.State.Status == model.StatusUp
	})

	healthy.Store(false)
	checkNowEventually(t, s, id)
	waitFor(t, 3*time.Second, func() bool {
		current, err := st.MonitorByID(t.Context(), id)
		return err == nil && current.State.Status == model.StatusDown
	})

	checks, err := st.RecentCheckSummaries(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentCheckSummaries: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(checks))
	}
	events, err := st.RecentEvents(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != model.EventDown {
		t.Fatalf("events = %+v", events)
	}
}
