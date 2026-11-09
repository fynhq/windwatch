package notify

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

func TestOutboxDispatchesAndClears(t *testing.T) {
	service, st := testNotifyService(t)
	hits := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	channel := createTestChannel(t, service, st, "Outbox hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)

	ctx, cancel := context.WithCancel(context.Background())
	service.Start(ctx)
	t.Cleanup(func() {
		cancel()
		service.Wait()
	})

	if !service.Enqueue(model.Notification{
		Key: "outbox:dispatch", Kind: model.EventDown, MonitorID: monitorID,
		MonitorName: "Outbox", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "boom", OccurredAt: time.Now().UTC(),
	}) {
		t.Fatal("Enqueue returned false")
	}
	select {
	case <-hits:
	case <-time.After(2 * time.Second):
		t.Fatal("outbox notification was not dispatched")
	}
	waitOutboxEmpty(t, st)
}

func TestOutboxSurvivesServiceRestart(t *testing.T) {
	service, st := testNotifyService(t)
	hits := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	channel := createTestChannel(t, service, st, "Restart hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)

	if !service.Enqueue(model.Notification{
		Key: "outbox:restart", Kind: model.EventDown, MonitorID: monitorID,
		MonitorName: "Outbox", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "boom", OccurredAt: time.Now().UTC(),
	}) {
		t.Fatal("Enqueue returned false")
	}

	restarted := New(st, service.cipher, service.catalogs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	restarted.Start(ctx)
	t.Cleanup(func() {
		cancel()
		restarted.Wait()
	})
	select {
	case <-hits:
	case <-time.After(2 * time.Second):
		t.Fatal("outbox notification did not survive service restart")
	}
	waitOutboxEmpty(t, st)
}

func TestOutboxDeduplicatesSameKey(t *testing.T) {
	service, st := testNotifyService(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	channel := createTestChannel(t, service, st, "Dedupe hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)

	ctx, cancel := context.WithCancel(context.Background())
	service.Start(ctx)
	t.Cleanup(func() {
		cancel()
		service.Wait()
	})

	notification := model.Notification{
		Key: "outbox:duplicate", Kind: model.EventDown, MonitorID: monitorID,
		MonitorName: "Outbox", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "boom", OccurredAt: time.Now().UTC(),
	}
	service.Enqueue(notification)
	service.Enqueue(notification)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		waitUntilOutboxEmpty(t, st)
		if hits.Load() == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() != 1 {
		t.Fatalf("webhook hits = %d, want 1", hits.Load())
	}
}

func TestStartupRecoveryRespectsPendingBackoff(t *testing.T) {
	service, st := testNotifyService(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	channel := createTestChannel(t, service, st, "Backoff hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	now := time.Now().UTC()
	_, _, err := st.ClaimNotificationID(t.Context(), "backoff:pending", string(channel.Type), channel.ID, channel.Name, channel.NotificationTarget(), `{"event":"down"}`, "Example", now.Add(-5*time.Minute))
	if err != nil {
		t.Fatalf("ClaimNotificationID: %v", err)
	}
	if err := st.ScheduleNotificationRetry(t.Context(), "backoff:pending", string(channel.Type), channel.ID, channel.NotificationTarget(), "retry later", 1, now.Add(time.Hour)); err != nil {
		t.Fatalf("ScheduleNotificationRetry: %v", err)
	}
	entries, err := st.PendingNotifications(t.Context(), pendingRecoveryCap)
	if err != nil {
		t.Fatalf("PendingNotifications: %v", err)
	}
	service.recoverPendingEntries(t.Context(), entries)
	if hits.Load() != 0 {
		t.Fatalf("recovery bypassed pending backoff: hits=%d", hits.Load())
	}
}

func waitOutboxEmpty(t *testing.T, st *store.Store) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if waitUntilOutboxEmpty(t, st) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("notification outbox was not cleared")
}

func waitUntilOutboxEmpty(t *testing.T, st *store.Store) bool {
	t.Helper()
	entries, err := st.DueNotificationOutbox(context.Background(), time.Now().UTC().Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("DueNotificationOutbox: %v", err)
	}
	return len(entries) == 0
}
