package notify

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestLongRetryAfterDefersWithoutConsumingAttempt(t *testing.T) {
	service, st := testNotifyService(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	channel := createTestChannel(t, service, st, "Flood hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)
	notification := model.Notification{
		Key: fmt.Sprintf("down:%d:1", monitorID), Kind: model.EventDown, MonitorID: monitorID,
		MonitorName: "Example", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "flood", OccurredAt: time.Now().UTC(),
	}
	service.dispatch(t.Context(), notification)

	entry := latestNotification(t, st)
	if entry.Status != "pending" || entry.Attempts != 0 {
		t.Fatalf("long retry-after entry = status %q attempts %d, want pending with no attempt consumed", entry.Status, entry.Attempts)
	}
	wait := entry.NextAttemptAt.Sub(time.Now().UTC())
	if wait < 9*time.Minute || wait > 11*time.Minute {
		t.Fatalf("next attempt in %s, want about 10 minutes", wait)
	}
}

func TestShortRetryAfterConsumesAttempt(t *testing.T) {
	service, st := testNotifyService(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	channel := createTestChannel(t, service, st, "Short hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)
	notification := model.Notification{
		Key: fmt.Sprintf("down:%d:1", monitorID), Kind: model.EventDown, MonitorID: monitorID,
		MonitorName: "Example", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "brief", OccurredAt: time.Now().UTC(),
	}
	service.dispatch(t.Context(), notification)

	entry := latestNotification(t, st)
	if entry.Status != "pending" || entry.Attempts != 1 {
		t.Fatalf("short retry-after entry = status %q attempts %d, want pending attempts 1", entry.Status, entry.Attempts)
	}
	wait := entry.NextAttemptAt.Sub(time.Now().UTC())
	if wait < 2*time.Second || wait > 7*time.Second {
		t.Fatalf("next attempt in %s, want about 5 seconds", wait)
	}
}

func TestRetryAfterBeyondWindowFails(t *testing.T) {
	service, st := testNotifyService(t)
	service.retryMaxAge = time.Minute
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	channel := createTestChannel(t, service, st, "Window hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)
	notification := model.Notification{
		Key: fmt.Sprintf("down:%d:1", monitorID), Kind: model.EventDown, MonitorID: monitorID,
		MonitorName: "Example", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "window", OccurredAt: time.Now().UTC(),
	}
	service.dispatch(t.Context(), notification)

	entry := latestNotification(t, st)
	if entry.Status != "failed" {
		t.Fatalf("retry-after beyond window entry = status %q, want failed", entry.Status)
	}
}

func TestRequestTimeoutIsRetryable(t *testing.T) {
	service, st := testNotifyService(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestTimeout)
	}))
	defer server.Close()
	channel := createTestChannel(t, service, st, "Timeout hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)
	notification := model.Notification{
		Key: fmt.Sprintf("down:%d:1", monitorID), Kind: model.EventDown, MonitorID: monitorID,
		MonitorName: "Example", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "timeout", OccurredAt: time.Now().UTC(),
	}
	service.dispatch(t.Context(), notification)

	entry := latestNotification(t, st)
	if entry.Status != "pending" || entry.Attempts != 1 {
		t.Fatalf("408 entry = status %q attempts %d, want pending attempts 1", entry.Status, entry.Attempts)
	}
	if !strings.Contains(entry.Error, "HTTP 408") {
		t.Fatalf("408 entry error = %q", entry.Error)
	}
}
