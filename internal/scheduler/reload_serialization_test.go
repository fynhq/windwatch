package scheduler

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/checker"
	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

func newReloadFixture(t *testing.T) (*Scheduler, *store.Store, int64, time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "reload.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	id, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "Reload", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: time.Second, FailThreshold: 1, RecoverThreshold: 1,
		Enabled: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	s := New(st, checker.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		s.Wait()
	})
	return s, st, id, now
}

func setInterval(t *testing.T, st *store.Store, id int64, interval time.Duration, now time.Time) {
	t.Helper()
	monitor, err := st.MonitorConfigByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorConfigByID: %v", err)
	}
	monitor.Interval = interval
	if err := st.UpdateMonitor(t.Context(), monitor, now); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}
}

func runnerSnapshot(s *Scheduler, id int64) (runnerHandle, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	handle := s.runners[id]
	if handle == nil {
		return runnerHandle{}, false
	}
	return *handle, true
}

func TestReloadUsesLatestConfig(t *testing.T) {
	s, st, id, now := newReloadFixture(t)
	setInterval(t, st, id, 2*time.Minute, now)
	if err := s.Reload(id); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	handle, ok := runnerSnapshot(s, id)
	if !ok || handle.interval != 2*time.Minute || handle.push {
		t.Fatalf("runner = %+v (present=%v), want interval 2m push=false", handle, ok)
	}
}

func TestConcurrentReloadsAndTogglesKeepLatestRunner(t *testing.T) {
	s, st, id, now := newReloadFixture(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				setInterval(t, st, id, time.Duration(60+g*10)*time.Second, now)
				if err := s.Reload(id); err != nil {
					t.Errorf("Reload: %v", err)
					return
				}
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				if err := s.SetMonitorEnabled(t.Context(), id, i%2 == 0); err != nil {
					t.Errorf("SetMonitorEnabled: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if err := s.SetMonitorEnabled(t.Context(), id, true); err != nil {
		t.Fatalf("final enable: %v", err)
	}
	setInterval(t, st, id, 5*time.Minute, now)
	if err := s.Reload(id); err != nil {
		t.Fatalf("final Reload: %v", err)
	}
	handle, ok := runnerSnapshot(s, id)
	if !ok || handle.interval != 5*time.Minute {
		t.Fatalf("final runner = %+v (present=%v), want interval 5m", handle, ok)
	}
}
