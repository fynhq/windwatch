package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

func TestTransportErrorKeepsCauseWithoutURL(t *testing.T) {
	err := transportError("webhook request failed", &url.Error{
		Op:  http.MethodPost,
		URL: "https://oapi.dingtalk.com/robot/send?access_token=secret-token",
		Err: errors.New("dial tcp 127.0.0.1:1: connect: connection refused"),
	})
	text := err.Error()
	if !strings.Contains(text, "connection refused") {
		t.Fatalf("transport error lost cause: %q", text)
	}
	if strings.Contains(text, "secret-token") || strings.Contains(text, "access_token") {
		t.Fatalf("transport error leaked request URL: %q", text)
	}
}

func TestStateMonitorID(t *testing.T) {
	tests := []struct {
		key string
		id  int64
		ok  bool
	}{
		{key: "down:12:34", id: 12, ok: true},
		{key: "up:3:4", id: 3, ok: true},
		{key: "remind_down:7:8:1", id: 7, ok: true},
		{key: "remind_warning:7:8:1", ok: false},
		{key: "warn_cert:1:fp:14", ok: false},
		{key: "warn_domain:example.com:1:30", ok: false},
		{key: "down::1", ok: false},
	}
	for _, test := range tests {
		id, ok := stateMonitorID(test.key)
		if ok != test.ok || id != test.id {
			t.Fatalf("stateMonitorID(%q) = %d,%v; want %d,%v", test.key, id, ok, test.id, test.ok)
		}
	}
}

func TestFailedNotificationIsScheduledAndRetried(t *testing.T) {
	service, st := testNotifyService(t)
	service.retryDelays = nil
	service.retrySchedule = func(int) time.Duration { return 0 }
	service.retryMaxAttempts = 3
	service.retryMaxAge = time.Hour

	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	channel := createTestChannel(t, service, st, "Retry hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)
	notification := model.Notification{
		Key:           fmt.Sprintf("down:%d:100", monitorID),
		Kind:          model.EventDown,
		MonitorID:     monitorID,
		MonitorName:   "Example",
		MonitorTarget: "https://example.com",
		MonitorType:   model.MonitorTypeHTTP,
		Status:        model.StatusDown,
		Message:       "connection refused",
		OccurredAt:    time.Now().UTC(),
	}
	service.dispatch(t.Context(), notification)

	entry := latestNotification(t, st)
	if entry.Status != "pending" || entry.Attempts != 1 {
		t.Fatalf("notification after failure = status %q attempts %d", entry.Status, entry.Attempts)
	}
	if !strings.Contains(entry.Error, "webhook returned HTTP 500") {
		t.Fatalf("stored error = %q", entry.Error)
	}
	if entry.NextAttemptAt.IsZero() {
		t.Fatal("retry was not scheduled")
	}

	fail.Store(false)
	service.retryDue(t.Context())
	entry = latestNotification(t, st)
	if entry.Status != "sent" || entry.Error != "" {
		t.Fatalf("notification after retry = status %q error %q", entry.Status, entry.Error)
	}
}

func TestFailedDownRetainsOrderBeforeUp(t *testing.T) {
	service, st := testNotifyService(t)
	service.retryDelays = nil
	service.retrySchedule = func(int) time.Duration { return 0 }
	service.retryMaxAttempts = 3
	service.retryMaxAge = time.Hour

	var failDown atomic.Bool
	failDown.Store(true)
	var mu sync.Mutex
	var events []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Event string `json:"event"`
		}
		_ = json.Unmarshal(body, &payload)
		mu.Lock()
		events = append(events, payload.Event)
		mu.Unlock()
		if payload.Event == "down" && failDown.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	channel := createTestChannel(t, service, st, "Ordering hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)
	now := time.Now().UTC()
	down := model.Notification{
		Key: fmt.Sprintf("down:%d:100", monitorID), Kind: model.EventDown, MonitorID: monitorID,
		MonitorName: "Example", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "connection refused", OccurredAt: now,
	}
	up := model.Notification{
		Key: fmt.Sprintf("up:%d:200", monitorID), Kind: model.EventUp, MonitorID: monitorID,
		MonitorName: "Example", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusUp, OccurredAt: now, Downtime: time.Minute, DowntimeStart: now.Add(-time.Minute), DowntimeCycles: 1,
	}
	service.dispatch(t.Context(), down)
	service.dispatch(t.Context(), up)

	mu.Lock()
	sent := append([]string(nil), events...)
	mu.Unlock()
	if !reflect.DeepEqual(sent, []string{"down"}) {
		t.Fatalf("events before retry = %v, want [down]", sent)
	}
	upEntry := notificationByKey(t, st, up.Key)
	if upEntry.Status != "pending" {
		t.Fatalf("up status before retry = %q, want pending", upEntry.Status)
	}

	failDown.Store(false)
	service.retryDue(t.Context())

	mu.Lock()
	sent = append([]string(nil), events...)
	mu.Unlock()
	if !reflect.DeepEqual(sent, []string{"down", "down", "up"}) {
		t.Fatalf("events after retry = %v, want [down down up]", sent)
	}
	if entry := notificationByKey(t, st, down.Key); entry.Status != "sent" {
		t.Fatalf("down status = %q, want sent", entry.Status)
	}
	if entry := notificationByKey(t, st, up.Key); entry.Status != "sent" {
		t.Fatalf("up status = %q, want sent", entry.Status)
	}
}

func TestNotificationRetryStopsAtAttemptLimit(t *testing.T) {
	service, st := testNotifyService(t)
	service.retryDelays = nil
	service.retrySchedule = func(int) time.Duration { return 0 }
	service.retryMaxAttempts = 2
	service.retryMaxAge = time.Hour

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	channel := createTestChannel(t, service, st, "Limit hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)
	service.dispatch(t.Context(), notificationForMonitor(monitorID, time.Now().UTC()))

	entry := latestNotification(t, st)
	if entry.Status != "pending" || entry.Attempts != 1 {
		t.Fatalf("notification after first failure = status %q attempts %d", entry.Status, entry.Attempts)
	}
	service.retryDue(t.Context())
	entry = latestNotification(t, st)
	if entry.Status != "failed" || entry.Attempts != 2 {
		t.Fatalf("notification after limit = status %q attempts %d", entry.Status, entry.Attempts)
	}
	if !strings.Contains(entry.Error, "webhook returned HTTP 500") {
		t.Fatalf("stored error = %q", entry.Error)
	}
}

func TestPermanentNotificationFailureIsNotRetried(t *testing.T) {
	service, st := testNotifyService(t)
	service.retryDelays = nil
	service.retrySchedule = func(int) time.Duration { return 0 }
	service.retryMaxAttempts = 3
	service.retryMaxAge = time.Hour

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	channel := createTestChannel(t, service, st, "Permanent hook", model.NotificationChannelWebhook, "", server.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)
	service.dispatch(t.Context(), notificationForMonitor(monitorID, time.Now().UTC()))

	entry := latestNotification(t, st)
	if entry.Status != "failed" || entry.Attempts != 1 {
		t.Fatalf("notification after permanent failure = status %q attempts %d", entry.Status, entry.Attempts)
	}
	if hits.Load() != 1 {
		t.Fatalf("permanent failure hits = %d, want 1", hits.Load())
	}
}

func notificationForMonitor(monitorID int64, now time.Time) model.Notification {
	return model.Notification{
		Key: fmt.Sprintf("down:%d:%d", monitorID, now.UnixMilli()), Kind: model.EventDown, MonitorID: monitorID,
		MonitorName: "Example", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "connection refused", OccurredAt: now,
	}
}

func latestNotification(t *testing.T, st *store.Store) model.NotificationLog {
	t.Helper()
	logs, err := st.RecentNotifications(t.Context(), 10)
	if err != nil {
		t.Fatalf("RecentNotifications: %v", err)
	}
	if len(logs) == 0 {
		t.Fatal("no notification logs")
	}
	return logs[0]
}

func notificationByKey(t *testing.T, st *store.Store, key string) model.NotificationLog {
	t.Helper()
	logs, err := st.RecentNotifications(t.Context(), 10)
	if err != nil {
		t.Fatalf("RecentNotifications: %v", err)
	}
	for _, entry := range logs {
		if entry.DedupeKey == key {
			return entry
		}
	}
	t.Fatalf("notification %q not found", key)
	return model.NotificationLog{}
}
