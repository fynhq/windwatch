package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/checker"
	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/notify"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

func TestNotifierDispatchesOutboxWrittenAfterSchedulerStop(t *testing.T) {
	received := make(chan string, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "drain.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().UTC()
	channelID, err := st.CreateNotificationChannel(t.Context(), model.NotificationChannel{
		Name: "hook", Type: model.NotificationChannelWebhook, Language: "en", Enabled: true,
		WebhookURL: receiver.URL, WebhookFormat: model.WebhookFormatGeneric,
	}, now)
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	id, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "drain", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: target.URL, Method: "GET", ExpectedStatus: "200-299"},
		Interval: 20 * time.Second, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1, Enabled: true, NotifyEnabled: true,
		NotificationChannelIDs: []int64{channelID},
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, err := secret.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := notify.New(st, cipher, catalogs, logger)
	s := New(st, checker.NewRegistry(), logger)
	s.SetCipher(cipher)
	s.SetNotifier(notifier)

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	notifyCtx, cancelNotify := context.WithCancel(context.Background())
	notifier.Start(notifyCtx)
	if err := s.Start(schedulerCtx); err != nil {
		cancelScheduler()
		cancelNotify()
		notifier.Wait()
		t.Fatalf("Start: %v", err)
	}

	if err := s.CheckNow(id); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	select {
	case payload := <-received:
		if !strings.Contains(payload, "drain") {
			t.Fatalf("first notification payload = %s", payload)
		}
	case <-time.After(20 * time.Second):
		cancelScheduler()
		s.Wait()
		cancelNotify()
		notifier.Wait()
		t.Fatal("first DOWN notification was not dispatched")
	}

	cancelScheduler()
	s.Wait()

	second := model.Notification{
		Key: "down:drain-second", Kind: model.EventDown, MonitorID: id, MonitorName: "drain",
		MonitorTarget: target.URL, MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "second-intent", OccurredAt: time.Now().UTC(),
	}
	if err := st.EnqueueNotificationOutbox(context.Background(), second, time.Now().UTC()); err != nil {
		t.Fatalf("EnqueueNotificationOutbox: %v", err)
	}
	notifier.Wake()
	select {
	case payload := <-received:
		if !strings.Contains(payload, "second-intent") {
			t.Fatalf("drain notification payload = %s", payload)
		}
	case <-time.After(10 * time.Second):
		cancelNotify()
		notifier.Wait()
		t.Fatal("outbox intent written after scheduler stop was not dispatched before notifier stop")
	}

	cancelNotify()
	notifier.Wait()
}
