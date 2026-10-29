package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
)

func TestTelegramRetryAfterParsed(t *testing.T) {
	service, _ := testNotifyService(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Too Many Requests","parameters":{"retry_after":2}}`))
	}))
	defer server.Close()
	service.telegramBaseURL = server.URL

	err := service.postTelegram(context.Background(), http.DefaultClient, "token", "111", "hello")
	var retryAfter retryAfterError
	if !errors.As(err, &retryAfter) || retryAfter.wait != 2*time.Second {
		t.Fatalf("retry_after error = %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if value := parseRetryAfter("7", now); value != 7*time.Second {
		t.Fatalf("seconds = %s", value)
	}
	if value := parseRetryAfter("Sat, 24 Oct 2026 12:00:30 GMT", now); value != 30*time.Second {
		t.Fatalf("date = %s", value)
	}
	if value := parseRetryAfter("not-a-date", now); value != 0 {
		t.Fatalf("invalid = %s", value)
	}
}

func TestRetryHonorsRetryAfter(t *testing.T) {
	service, _ := testNotifyService(t)
	attempts := 0
	started := time.Now()
	err := service.retry(context.Background(), func() error {
		attempts++
		if attempts == 1 {
			return retryAfterError{wait: 20 * time.Millisecond}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond {
		t.Fatalf("retry_after was not honored: %s", elapsed)
	}
}

func TestPendingRecovery(t *testing.T) {
	service, st := testNotifyService(t)
	var (
		mu            sync.Mutex
		telegramTexts []string
		webhookHits   []string
	)
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		telegramTexts = append(telegramTexts, payload.ChatID+":"+payload.Text)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer telegram.Close()
	webhookSecret := "recover-secret"
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(webhookSecret))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("X-WindWatch-Signature") != want {
			t.Errorf("recovery signature = %q, want %q", r.Header.Get("X-WindWatch-Signature"), want)
		}
		mu.Lock()
		webhookHits = append(webhookHits, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	service.telegramBaseURL = telegram.URL
	telegramChannel := createTestChannel(t, service, st, "Telegram recovery", model.NotificationChannelTelegram, "111", "", "token", true)
	webhookChannel := createTestChannel(t, service, st, "Webhook recovery", model.NotificationChannelWebhook, "", webhook.URL, webhookSecret, true)

	now := time.Now().UTC()
	if _, err := st.ClaimNotification(t.Context(), "pending:telegram", string(telegramChannel.Type), telegramChannel.ID, telegramChannel.Name, "111", "recovered text", now.Add(-time.Minute)); err != nil {
		t.Fatalf("claim telegram: %v", err)
	}
	if _, err := st.ClaimNotification(t.Context(), "pending:webhook", string(webhookChannel.Type), webhookChannel.ID, webhookChannel.Name, webhookChannel.NotificationTarget(), `{"version":1,"event":"down"}`, now.Add(-time.Minute)); err != nil {
		t.Fatalf("claim webhook: %v", err)
	}
	if _, err := st.ClaimNotification(t.Context(), "pending:expired", string(telegramChannel.Type), telegramChannel.ID, telegramChannel.Name, "111", "expired text", now.Add(-25*time.Hour)); err != nil {
		t.Fatalf("claim expired: %v", err)
	}
	if _, err := st.ClaimNotification(t.Context(), "pending:empty", string(webhookChannel.Type), webhookChannel.ID, webhookChannel.Name, webhookChannel.NotificationTarget(), "", now.Add(-time.Minute)); err != nil {
		t.Fatalf("claim empty: %v", err)
	}

	service.recoverPending(t.Context())
	mu.Lock()
	telegramCount := len(telegramTexts)
	webhookCount := len(webhookHits)
	mu.Unlock()
	if telegramCount != 1 || !strings.Contains(telegramTexts[0], "recovered text") {
		t.Fatalf("telegram recovery hits = %v", telegramTexts)
	}
	if webhookCount != 1 {
		t.Fatalf("webhook recovery hits = %v", webhookHits)
	}

	logs, err := st.RecentNotifications(t.Context(), 10)
	if err != nil {
		t.Fatalf("RecentNotifications: %v", err)
	}
	statuses := make(map[string]string)
	for _, entry := range logs {
		statuses[entry.DedupeKey] = entry.Status
	}
	if statuses["pending:telegram"] != "sent" || statuses["pending:webhook"] != "sent" {
		t.Fatalf("recovered statuses = %+v", statuses)
	}
	if statuses["pending:expired"] != "failed" || statuses["pending:empty"] != "failed" {
		t.Fatalf("invalid pending statuses = %+v", statuses)
	}
}

func TestRenderUsesConfiguredTimezone(t *testing.T) {
	service, _ := testNotifyService(t)
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	service.catalogs = catalogs
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	notification := model.Notification{
		Kind:        model.EventDown,
		MonitorName: "Example",
		Status:      model.StatusDown,
		Message:     "connection refused",
		OccurredAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	text := service.render(i18n.LangEN, notification, location)
	if !strings.Contains(text, "2026-01-01 08:00:00") {
		t.Fatalf("notification time not converted: %q", text)
	}
}

func TestRenderStatusEmoji(t *testing.T) {
	service, _ := testNotifyService(t)
	now := time.Now().UTC()
	down := model.Notification{
		Kind: model.EventDown, MonitorName: "Example", MonitorTarget: "https://example.com",
		MonitorType: model.MonitorTypeHTTP, Status: model.StatusDown,
		Message: "connection refused", OccurredAt: now,
	}
	up := model.Notification{
		Kind: model.EventUp, MonitorName: "Example", MonitorTarget: "https://example.com",
		MonitorType: model.MonitorTypeHTTP, Status: model.StatusUp,
		OccurredAt: now, DowntimeStart: now.Add(-time.Minute), DowntimeCycles: 1,
	}
	for _, language := range []string{i18n.LangEN, i18n.LangZH} {
		if text := service.render(language, down); !strings.HasPrefix(text, "🔴 DOWN") {
			t.Fatalf("%s down text = %q", language, text)
		}
		if text := service.render(language, up); !strings.HasPrefix(text, "✅ UP") {
			t.Fatalf("%s up text = %q", language, text)
		}
	}
}

func TestRenderRecoveryShowsObservedWindow(t *testing.T) {
	service, _ := testNotifyService(t)
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	start := time.Date(2026, 10, 28, 10, 58, 27, 0, time.UTC)
	end := time.Date(2026, 10, 28, 11, 0, 31, 0, time.UTC)
	notification := model.Notification{
		Kind:           model.EventUp,
		MonitorName:    "Proxy",
		MonitorTarget:  "https://google.com",
		MonitorType:    model.MonitorTypeHTTP,
		Status:         model.StatusUp,
		OccurredAt:     end,
		DowntimeStart:  start,
		Downtime:       end.Sub(start),
		DowntimeCycles: 2,
	}

	zh := service.render(i18n.LangZH, notification, location)
	if !strings.Contains(zh, "故障持续: 2 个检测周期（2026-10-28 18:58:27 – 19:00:31）") {
		t.Fatalf("zh recovery text = %q", zh)
	}
	if strings.Contains(zh, "时间:") {
		t.Fatalf("zh recovery text still has a separate time line: %q", zh)
	}

	en := service.render(i18n.LangEN, notification, location)
	if !strings.Contains(en, "Downtime: 2 check intervals (2026-10-28 18:58:27 – 19:00:31)") {
		t.Fatalf("en recovery text = %q", en)
	}

	body, err := service.webhookBody(notification, en)
	if err != nil {
		t.Fatalf("webhookBody: %v", err)
	}
	var payload struct {
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Details["downtime_seconds"] != float64(124) ||
		payload.Details["downtime_cycles"] != float64(2) ||
		payload.Details["downtime_started_at"] != "2026-10-28T10:58:27Z" {
		t.Fatalf("recovery details = %+v", payload.Details)
	}
}

func TestReminderRenderPrefixAndWebhookDetails(t *testing.T) {
	service, _ := testNotifyService(t)
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	service.catalogs = catalogs
	notification := model.Notification{
		Key: "remind_down:1:1:1", Kind: model.EventDown, MonitorID: 1, MonitorName: "Example",
		MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "connection refused", OccurredAt: time.Now().UTC(),
		Reminder: 1, ReminderTotal: 2,
	}
	text := service.render(i18n.LangEN, notification)
	if !strings.Contains(text, "[REMINDER 1/2]") || !strings.Contains(text, "connection refused") {
		t.Fatalf("reminder text = %q", text)
	}
	body, err := service.webhookBody(notification, text)
	if err != nil {
		t.Fatalf("webhookBody: %v", err)
	}
	var payload struct {
		Details map[string]any `json:"details"`
		Text    string         `json:"text"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Details["reminder"] != true || payload.Details["reminder_number"] != float64(1) ||
		payload.Details["reminder_total"] != float64(2) || !strings.Contains(payload.Text, "REMINDER") {
		t.Fatalf("payload = %+v", payload)
	}
}
