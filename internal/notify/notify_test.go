package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

func testNotifyService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	cipher, err := secret.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	return New(st, cipher, catalogs, slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func createTestChannel(t *testing.T, service *Service, st *store.Store, name string, channelType model.NotificationChannelType, chatIDs, webhookURL, plainSecret string, enabled bool) model.NotificationChannel {
	t.Helper()
	channel := model.NotificationChannel{
		Name:            name,
		Type:            channelType,
		Enabled:         enabled,
		TelegramChatIDs: chatIDs,
		WebhookURL:      webhookURL,
	}
	if plainSecret != "" {
		encrypted, err := service.cipher.Encrypt(plainSecret)
		if err != nil {
			t.Fatalf("encrypt channel secret: %v", err)
		}
		channel.SecretEnc = encrypted
	}
	id, err := st.CreateNotificationChannel(t.Context(), channel, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	channel.ID = id
	return channel
}

func createTestMonitor(t *testing.T, st *store.Store, channelIDs ...int64) int64 {
	t.Helper()
	id, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "Example", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1, Enabled: true, NotifyEnabled: true,
		NotificationChannelIDs: channelIDs,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return id
}

func TestTelegramTestAndDispatchDedupe(t *testing.T) {
	service, st := testNotifyService(t)
	var telegramHits atomic.Int32
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/sendMessage") {
			t.Errorf("unexpected Telegram path %s", r.URL.Path)
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["chat_id"] == nil || payload["text"] == nil {
			t.Errorf("missing Telegram payload: %+v", payload)
		}
		telegramHits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer telegram.Close()
	service.telegramBaseURL = telegram.URL
	channel := createTestChannel(t, service, st, "Telegram main", model.NotificationChannelTelegram, "111,222", "", "token", true)

	if err := service.Test(t.Context(), channel.ID); err != nil {
		t.Fatalf("Telegram Test: %v", err)
	}
	if telegramHits.Load() != 2 {
		t.Fatalf("telegram hits = %d, want 2", telegramHits.Load())
	}

	monitorID := createTestMonitor(t, st, channel.ID)
	notification := model.Notification{
		Key:           "down:1:100",
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
	service.dispatch(t.Context(), notification)
	if telegramHits.Load() != 4 {
		t.Fatalf("telegram hits after two dispatches = %d, want 4 (2 chats once)", telegramHits.Load())
	}
	logs, err := st.RecentNotifications(t.Context(), 10)
	if err != nil || len(logs) != 2 {
		t.Fatalf("notification logs=%+v err=%v", logs, err)
	}
	for _, entry := range logs {
		if entry.ChannelName != channel.Name || entry.ChannelID != channel.ID {
			t.Fatalf("log channel = %+v", entry)
		}
	}
}

func TestChannelLanguageControlsRenderedMessage(t *testing.T) {
	service, st := testNotifyService(t)
	texts := make(chan string, 3)
	newServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var payload webhookPayload
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("decode payload: %v", err)
			}
			texts <- payload.Text
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	enServer := newServer()
	defer enServer.Close()
	zhServer := newServer()
	defer zhServer.Close()

	enChannel := createTestChannel(t, service, st, "English hook", model.NotificationChannelWebhook, "", enServer.URL, "", true)
	enChannel.Language = model.NotificationLanguageEN
	if err := st.UpdateNotificationChannel(t.Context(), enChannel, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateNotificationChannel en: %v", err)
	}
	zhChannel := createTestChannel(t, service, st, "Chinese hook", model.NotificationChannelWebhook, "", zhServer.URL, "", true)
	zhChannel.Language = model.NotificationLanguageZH
	if err := st.UpdateNotificationChannel(t.Context(), zhChannel, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateNotificationChannel zh: %v", err)
	}

	monitorID := createTestMonitor(t, st, enChannel.ID, zhChannel.ID)
	notification := model.Notification{
		Key:           "down:language:1",
		Kind:          model.EventDown,
		MonitorID:     monitorID,
		MonitorName:   "Language",
		MonitorTarget: "https://example.com",
		MonitorType:   model.MonitorTypeHTTP,
		Status:        model.StatusDown,
		Message:       "connection refused",
		OccurredAt:    time.Now().UTC(),
	}
	service.dispatch(t.Context(), notification)

	var english, chinese string
	for i := 0; i < 2; i++ {
		text := <-texts
		if strings.Contains(text, "Target:") {
			english = text
		}
		if strings.Contains(text, "目标:") {
			chinese = text
		}
	}
	if english == "" || chinese == "" || strings.Contains(english, "目标:") || strings.Contains(chinese, "Target:") {
		t.Fatalf("per-channel languages not applied: english=%q chinese=%q", english, chinese)
	}

	enTest := createTestChannel(t, service, st, "English test hook", model.NotificationChannelWebhook, "", enServer.URL, "", true)
	enTest.Language = model.NotificationLanguageEN
	if err := st.UpdateNotificationChannel(t.Context(), enTest, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateNotificationChannel en test: %v", err)
	}
	if err := service.Test(t.Context(), enTest.ID); err != nil {
		t.Fatalf("Test en: %v", err)
	}
	if text := <-texts; !strings.Contains(text, "notification test") {
		t.Fatalf("test message language = %q", text)
	}
}

func TestWebhookTestAndHMAC(t *testing.T) {
	service, st := testNotifyService(t)
	var hits atomic.Int32
	secretValue := "webhook-secret"
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		expected := hmac.New(sha256.New, []byte(secretValue))
		expected.Write(body)
		want := "sha256=" + hex.EncodeToString(expected.Sum(nil))
		if r.Header.Get("X-WindWatch-Signature") != want {
			t.Errorf("signature = %q, want %q", r.Header.Get("X-WindWatch-Signature"), want)
		}
		var payload webhookPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if payload.Event == "" {
			t.Errorf("missing event: %+v", payload)
		}
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()
	channel := createTestChannel(t, service, st, "Hook", model.NotificationChannelWebhook, "", webhook.URL, secretValue, true)

	if err := service.Test(t.Context(), channel.ID); err != nil {
		t.Fatalf("Webhook Test: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("webhook hits = %d", hits.Load())
	}

	monitorID := createTestMonitor(t, st, channel.ID)
	notification := model.Notification{
		Key: "warn_cert:1:fp:14", Kind: model.EventWarnCert, MonitorID: monitorID,
		MonitorName: "Example", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusWarning, OccurredAt: time.Now().UTC(),
		Cert: &model.CertificateInfo{Fingerprint: "fp", Issuer: "Test CA", NotAfter: time.Now().Add(10 * 24 * time.Hour)},
	}
	service.dispatch(t.Context(), notification)
	service.dispatch(t.Context(), notification)
	if hits.Load() != 2 {
		t.Fatalf("webhook hits after dedupe = %d, want 2", hits.Load())
	}
}

func TestChannelSecretEncrypted(t *testing.T) {
	service, st := testNotifyService(t)
	channel := createTestChannel(t, service, st, "Secret", model.NotificationChannelWebhook, "", "https://example.com/hook", "webhook-secret", true)
	if channel.SecretEnc == "" || channel.SecretEnc == "webhook-secret" {
		t.Fatal("channel secret was stored in plaintext")
	}
	loaded, err := st.NotificationChannelByID(t.Context(), channel.ID)
	if err != nil {
		t.Fatalf("NotificationChannelByID: %v", err)
	}
	plain, err := service.cipher.Decrypt(loaded.SecretEnc)
	if err != nil || plain != "webhook-secret" {
		t.Fatalf("decrypted secret=%q err=%v", plain, err)
	}
}

func TestWebhookDoesNotFollowRedirects(t *testing.T) {
	service, st := testNotifyService(t)
	var finalHits atomic.Int32
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		finalHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirect.Close()

	channel := createTestChannel(t, service, st, "Redirect", model.NotificationChannelWebhook, "", redirect.URL, "", true)
	if err := service.sendWebhookBody(t.Context(), channel, []byte(`{"event":"test"}`)); err == nil {
		t.Fatal("redirect response was treated as success")
	}
	if finalHits.Load() != 0 {
		t.Fatal("redirect target was called")
	}
}

func TestNotificationClientIgnoresEnvironmentProxy(t *testing.T) {
	service, _ := testNotifyService(t)
	client, cleanup, err := service.httpClient(t.Context(), nil)
	if err != nil {
		t.Fatalf("httpClient: %v", err)
	}
	defer cleanup()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("direct notification client inherited an environment proxy")
	}
}

func TestNotificationsEnabled(t *testing.T) {
	service, st := testNotifyService(t)
	enabled, err := service.NotificationsEnabled(t.Context())
	if err != nil || enabled {
		t.Fatalf("enabled=%v err=%v", enabled, err)
	}
	channel := createTestChannel(t, service, st, "Disabled", model.NotificationChannelTelegram, "111", "", "token", false)
	enabled, err = service.NotificationsEnabled(t.Context())
	if err != nil || enabled {
		t.Fatalf("disabled channel enabled=%v err=%v", enabled, err)
	}
	channel.Enabled = true
	if err := st.UpdateNotificationChannel(t.Context(), channel, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateNotificationChannel: %v", err)
	}
	enabled, err = service.NotificationsEnabled(t.Context())
	if err != nil || !enabled {
		t.Fatalf("enabled=%v err=%v", enabled, err)
	}
}

func TestDomainWarningWithoutExpiry(t *testing.T) {
	service, _ := testNotifyService(t)
	text := service.render(i18n.LangEN, model.Notification{
		Kind:        model.EventWarnDomain,
		MonitorName: "Example",
		OccurredAt:  time.Now().UTC(),
		Domain:      &model.DomainInfo{Domain: "example.com", Statuses: []string{"clientHold"}},
	})
	if !strings.Contains(text, "Expiration: unknown") {
		t.Fatalf("text = %q", text)
	}
}

func TestRecoveryDoesNotBlockNewNotifications(t *testing.T) {
	service, st := testNotifyService(t)
	slowStarted := make(chan struct{})
	release := make(chan struct{})
	fastHit := make(chan struct{})
	var once sync.Once
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "slow recovery") {
			once.Do(func() { close(slowStarted) })
			<-release
		} else {
			select {
			case <-fastHit:
			default:
				close(fastHit)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	now := time.Now().UTC()
	channel := createTestChannel(t, service, st, "Recovery hook", model.NotificationChannelWebhook, "", webhook.URL, "", true)
	monitorID := createTestMonitor(t, st, channel.ID)
	if ok, err := st.ClaimNotification(t.Context(), "recovery-slow", string(channel.Type), channel.ID, channel.Name, channel.NotificationTarget(), "slow recovery", "Example site", now); err != nil || !ok {
		t.Fatalf("ClaimNotification: ok=%v err=%v", ok, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	service.Start(ctx)
	t.Cleanup(func() {
		close(release)
		cancel()
		service.Wait()
		webhook.Close()
	})

	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("recovery did not start")
	}
	service.Enqueue(model.Notification{
		Key:           "fast:1:1",
		Kind:          model.EventDown,
		MonitorID:     monitorID,
		MonitorName:   "fast notification",
		MonitorTarget: "https://example.com",
		MonitorType:   model.MonitorTypeHTTP,
		Status:        model.StatusDown,
		OccurredAt:    now,
	})
	select {
	case <-fastHit:
	case <-time.After(2 * time.Second):
		t.Fatal("new notification was blocked by recovery")
	}
}
