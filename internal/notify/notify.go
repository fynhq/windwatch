package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
	outboundproxy "github.com/fynhq/windwatch/internal/proxy"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

const (
	maxRetryAfter = 30 * time.Second
)

type Service struct {
	store           *store.Store
	cipher          *secret.Cipher
	catalogs        *i18n.Catalogs
	log             *slog.Logger
	wake            chan struct{}
	wg              sync.WaitGroup
	telegramBaseURL string

	retryDelays      []time.Duration
	retrySchedule    func(completed int) time.Duration
	retryMaxAge      time.Duration
	retryMaxAttempts int
	retryInterval    time.Duration
	retryBatchSize   int
}

func New(st *store.Store, cipher *secret.Cipher, catalogs *i18n.Catalogs, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:            st,
		cipher:           cipher,
		catalogs:         catalogs,
		log:              logger,
		wake:             make(chan struct{}, 1),
		telegramBaseURL:  "https://api.telegram.org",
		retryDelays:      []time.Duration{time.Second, 5 * time.Second},
		retrySchedule:    defaultRetryDelay,
		retryMaxAge:      time.Hour,
		retryMaxAttempts: 24,
		retryInterval:    15 * time.Second,
		retryBatchSize:   20,
	}
}

func (s *Service) Start(ctx context.Context) {
	// Capture the pending list before the dispatch goroutine starts. Notifications
	// claimed afterwards are handled only by dispatch/retry, never by recovery.
	pending, err := s.store.PendingNotifications(ctx, pendingRecoveryCap)
	if err != nil {
		s.log.Error("load pending notifications", "error", err)
	}
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		s.runSafely("recovery", func() {
			s.recoverPendingEntries(ctx, pending)
		})
		s.retryLoop(ctx)
	}()
	go func() {
		defer s.wg.Done()
		s.runSafely("outbox", func() {
			s.outboxLoop(ctx)
		})
	}()
}

func (s *Service) recoverTask(task string) {
	if recovered := recover(); recovered != nil {
		s.log.Error("notification task panic", "task", task, "panic", recovered, "stack", string(debug.Stack()))
	}
}

func (s *Service) runSafely(task string, fn func()) {
	defer s.recoverTask(task)
	fn()
}

func (s *Service) Wait() {
	s.wg.Wait()
}

func (s *Service) Enqueue(notification model.Notification) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.EnqueueNotificationOutbox(ctx, notification, time.Now().UTC()); err != nil {
		s.log.Error("persist notification outbox", "monitor_id", notification.MonitorID, "event", notification.Kind, "error", err)
		return false
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return true
}

func (s *Service) NotificationsEnabled(ctx context.Context) (bool, error) {
	count, err := s.store.EnabledNotificationChannelCount(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Service) dispatch(ctx context.Context, notification model.Notification) error {
	config, err := LoadConfig(ctx, s.store)
	if err != nil {
		s.log.Error("load notification settings", "error", err)
		return err
	}
	channelIDs, err := s.store.NotificationChannelIDsForMonitor(ctx, notification.MonitorID)
	if err != nil {
		s.log.Error("load monitor notification channels", "monitor_id", notification.MonitorID, "error", err)
		return err
	}
	var channelErr error
	for _, channelID := range channelIDs {
		channel, err := s.store.NotificationChannelByID(ctx, channelID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			s.log.Warn("load notification channel", "channel_id", channelID, "error", err)
			if channelErr == nil {
				channelErr = err
			}
			continue
		}
		if !channel.Enabled {
			continue
		}
		if err := s.sendChannel(ctx, config, channel, notification); err != nil {
			s.log.Warn("send notification", "channel_id", channel.ID, "monitor_id", notification.MonitorID, "kind", notification.Kind, "error", err)
		}
	}
	return channelErr
}

func (s *Service) Test(ctx context.Context, channelID int64) error {
	channel, err := s.store.NotificationChannelByID(ctx, channelID)
	if err != nil {
		return err
	}
	translator := s.catalogs.Translator(channel.ResolvedLanguage())
	notification := model.Notification{
		Key:        "test:" + strconv.FormatInt(channel.ID, 10) + ":" + time.Now().UTC().Format(time.RFC3339Nano),
		Kind:       model.EventType("test"),
		Status:     model.StatusUp,
		OccurredAt: time.Now().UTC(),
	}
	switch channel.Type {
	case model.NotificationChannelTelegram:
		token, err := s.channelSecret(channel)
		if err != nil {
			return err
		}
		return s.sendTelegramText(ctx, channel, token, translator.T("notify.test"), "", "")
	case model.NotificationChannelWebhook:
		body, err := s.webhookBody(channel, notification, translator.T("notify.test"))
		if err != nil {
			return err
		}
		return s.sendWebhookBody(ctx, channel, body)
	default:
		return fmt.Errorf("unknown notification channel type %q", channel.Type)
	}
}

func (s *Service) sendChannel(ctx context.Context, config Config, channel model.NotificationChannel, notification model.Notification) error {
	language := channel.ResolvedLanguage()
	switch channel.Type {
	case model.NotificationChannelTelegram:
		text := s.render(language, notification, config.Timezone)
		token, err := s.channelSecret(channel)
		if err != nil {
			return err
		}
		return s.sendTelegramText(ctx, channel, token, text, notification.Key, notification.MonitorName)
	case model.NotificationChannelWebhook:
		body, err := s.webhookBody(channel, notification, s.render(language, notification, config.Timezone))
		if err != nil {
			return err
		}
		return s.sendWebhook(ctx, channel, notification.Key, body, notification.MonitorName)
	default:
		return fmt.Errorf("unknown notification channel type %q", channel.Type)
	}
}

func (s *Service) channelSecret(channel model.NotificationChannel) (string, error) {
	if channel.SecretEnc == "" {
		return "", nil
	}
	value, err := s.cipher.Decrypt(channel.SecretEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt notification channel secret: %w", err)
	}
	return value, nil
}

func (s *Service) sendTelegramText(ctx context.Context, channel model.NotificationChannel, token, text, dedupeKey, monitorName string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("telegram token is not configured")
	}
	chatIDs := channel.ChatIDs()
	if len(chatIDs) == 0 {
		return errors.New("telegram chat id is not configured")
	}
	var firstErr error
	for _, chatID := range chatIDs {
		if dedupeKey == "" {
			client, cleanup, err := s.httpClient(ctx, channel.ProxyID)
			if err != nil {
				return err
			}
			sendErr := s.retry(ctx, func() error {
				return s.postTelegram(ctx, client, token, chatID, text)
			})
			cleanup()
			if sendErr != nil && firstErr == nil {
				firstErr = sendErr
			}
			continue
		}

		now := time.Now().UTC()
		claimed, id, err := s.store.ClaimNotificationID(ctx, dedupeKey, string(channel.Type), channel.ID, channel.Name, chatID, text, monitorName, now)
		if err != nil {
			return err
		}
		if !claimed {
			s.log.Info("notification skipped as duplicate", "channel", channel.Type, "channel_id", channel.ID, "target", chatID, "dedupe_key", dedupeKey)
			continue
		}
		entry := model.NotificationLog{
			ID: id, DedupeKey: dedupeKey, Channel: string(channel.Type), ChannelID: channel.ID,
			ChannelName: channel.Name, MonitorName: monitorName, Target: chatID, CreatedAt: now,
		}
		if s.deferToEarlierState(ctx, entry) {
			continue
		}
		client, cleanup, err := s.httpClient(ctx, channel.ProxyID)
		if err != nil {
			s.log.Warn("notification send failed", "channel", channel.Type, "channel_id", channel.ID, "target", chatID, "dedupe_key", dedupeKey, "error", err)
			s.recordFailure(ctx, entry, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sendErr := s.retry(ctx, func() error {
			return s.postTelegram(ctx, client, token, chatID, text)
		})
		cleanup()
		if sendErr != nil {
			s.log.Warn("notification send failed", "channel", channel.Type, "channel_id", channel.ID, "target", chatID, "dedupe_key", dedupeKey, "error", sendErr)
			s.recordFailure(ctx, entry, sendErr)
			if firstErr == nil {
				firstErr = sendErr
			}
			continue
		}
		s.markSent(ctx, entry)
	}
	return firstErr
}

func (s *Service) postTelegram(ctx context.Context, client *http.Client, token, chatID, text string) error {
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return permanentError{err: err}
	}
	endpoint := s.telegramBaseURL + "/bot" + token + "/sendMessage"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return permanentError{err: errors.New("invalid Telegram request")}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return transportError("telegram request failed", err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	parseErr := json.Unmarshal(data, &result)
	if response.StatusCode == http.StatusTooManyRequests {
		if result.Parameters.RetryAfter > 0 {
			return retryAfterError{wait: time.Duration(result.Parameters.RetryAfter) * time.Second}
		}
		return fmt.Errorf("telegram returned HTTP %d", response.StatusCode)
	}
	if response.StatusCode >= 500 {
		return fmt.Errorf("telegram returned HTTP %d", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return permanentError{err: fmt.Errorf("telegram returned HTTP %d", response.StatusCode)}
	}
	if parseErr != nil {
		return permanentError{err: errors.New("invalid Telegram response")}
	}
	if !result.OK {
		message := result.Description
		if message == "" {
			message = "telegram rejected the message"
		}
		return permanentError{err: errors.New(message)}
	}
	return nil
}

func (s *Service) sendWebhook(ctx context.Context, channel model.NotificationChannel, dedupeKey string, body []byte, monitorName string) error {
	if strings.TrimSpace(channel.WebhookURL) == "" {
		return errors.New("webhook URL is not configured")
	}
	target := channel.NotificationTarget()
	now := time.Now().UTC()
	claimed, id, err := s.store.ClaimNotificationID(ctx, dedupeKey, string(channel.Type), channel.ID, channel.Name, target, string(body), monitorName, now)
	if err != nil {
		return err
	}
	if !claimed {
		s.log.Info("notification skipped as duplicate", "channel", channel.Type, "channel_id", channel.ID, "target", target, "dedupe_key", dedupeKey)
		return nil
	}
	entry := model.NotificationLog{
		ID: id, DedupeKey: dedupeKey, Channel: string(channel.Type), ChannelID: channel.ID,
		ChannelName: channel.Name, MonitorName: monitorName, Target: target, CreatedAt: now,
	}
	if s.deferToEarlierState(ctx, entry) {
		return nil
	}
	sendErr := s.sendWebhookBody(ctx, channel, body)
	if sendErr != nil {
		s.log.Warn("notification send failed", "channel", channel.Type, "channel_id", channel.ID, "target", target, "dedupe_key", dedupeKey, "error", sendErr)
		s.recordFailure(ctx, entry, sendErr)
		return sendErr
	}
	s.markSent(ctx, entry)
	return nil
}

func (s *Service) sendWebhookBody(ctx context.Context, channel model.NotificationChannel, body []byte) error {
	return s.retry(ctx, func() error {
		return s.sendWebhookAttempt(ctx, channel, body)
	})
}

func (s *Service) sendWebhookAttempt(ctx context.Context, channel model.NotificationChannel, body []byte) error {
	if strings.TrimSpace(channel.WebhookURL) == "" {
		return permanentError{err: errors.New("webhook URL is not configured")}
	}
	format := channel.ResolvedWebhookFormat()
	secretValue := ""
	if format != model.WebhookFormatWeCom {
		value, err := s.channelSecret(channel)
		if err != nil {
			return permanentError{err: fmt.Errorf("notification channel secret: %w", err)}
		}
		secretValue = value
	}
	client, cleanup, err := s.httpClient(ctx, channel.ProxyID)
	if err != nil {
		return err
	}
	defer cleanup()
	return s.sendWebhookOnce(ctx, channel, client, format, secretValue, body)
}

func (s *Service) sendWebhookOnce(ctx context.Context, channel model.NotificationChannel, client *http.Client, format model.WebhookFormat, secretValue string, body []byte) error {
	endpoint := channel.WebhookURL
	payload := body
	if secretValue != "" {
		switch format {
		case model.WebhookFormatDingTalk:
			signed, err := dingTalkSignedURL(endpoint, secretValue, time.Now())
			if err != nil {
				return permanentError{err: errors.New("invalid webhook request")}
			}
			endpoint = signed
		case model.WebhookFormatFeishu:
			signed, err := feishuSignedBody(payload, secretValue, time.Now())
			if err != nil {
				return permanentError{err: errors.New("invalid webhook request")}
			}
			payload = signed
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return permanentError{err: errors.New("invalid webhook request")}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "WindWatch-Notifier")
	if format == model.WebhookFormatGeneric && secretValue != "" {
		mac := hmac.New(sha256.New, []byte(secretValue))
		mac.Write(payload)
		request.Header.Set("X-WindWatch-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	response, err := client.Do(request)
	if err != nil {
		return transportError("webhook request failed", err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if response.StatusCode == http.StatusTooManyRequests {
		if delay := parseRetryAfter(response.Header.Get("Retry-After"), time.Now()); delay > 0 {
			return retryAfterError{wait: delay}
		}
		return fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	if response.StatusCode >= 500 {
		return fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return permanentError{err: fmt.Errorf("webhook returned HTTP %d", response.StatusCode)}
	}
	return webhookResponseError(format, responseBody)
}

func webhookResponseError(format model.WebhookFormat, body []byte) error {
	switch format {
	case model.WebhookFormatDingTalk, model.WebhookFormatWeCom:
		var result struct {
			ErrCode *int   `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		if err := json.Unmarshal(body, &result); err != nil || result.ErrCode == nil {
			return permanentError{err: errors.New("invalid webhook response")}
		}
		if *result.ErrCode != 0 {
			return permanentError{err: platformError(*result.ErrCode, result.ErrMsg)}
		}
	case model.WebhookFormatFeishu:
		var result struct {
			Code          *int   `json:"code"`
			Msg           string `json:"msg"`
			StatusCode    *int   `json:"StatusCode"`
			StatusMessage string `json:"StatusMessage"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return permanentError{err: errors.New("invalid webhook response")}
		}
		code := result.Code
		message := result.Msg
		if code == nil {
			code = result.StatusCode
			message = result.StatusMessage
		}
		if code == nil {
			return permanentError{err: errors.New("invalid webhook response")}
		}
		if *code != 0 {
			return permanentError{err: platformError(*code, message)}
		}
	}
	return nil
}

func platformError(code int, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("webhook returned error code %d", code)
	}
	return fmt.Errorf("webhook returned error code %d: %s", code, message)
}

func dingTalkSignedURL(rawURL, secret string, now time.Time) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	timestamp := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + secret))
	query := parsed.Query()
	query.Set("timestamp", timestamp)
	query.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func feishuSignedBody(body []byte, secret string, now time.Time) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
	payload["timestamp"] = timestamp
	payload["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return json.Marshal(payload)
}

type webhookPayload struct {
	Version    int            `json:"version"`
	Event      string         `json:"event"`
	Status     string         `json:"status"`
	Monitor    webhookMonitor `json:"monitor"`
	Message    string         `json:"message,omitempty"`
	Text       string         `json:"text"`
	OccurredAt string         `json:"occurred_at"`
	Details    map[string]any `json:"details,omitempty"`
}

type webhookMonitor struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

func (s *Service) webhookBody(channel model.NotificationChannel, notification model.Notification, text string) ([]byte, error) {
	switch channel.ResolvedWebhookFormat() {
	case model.WebhookFormatDingTalk, model.WebhookFormatWeCom:
		return json.Marshal(map[string]any{
			"msgtype": "text",
			"text":    map[string]string{"content": text},
		})
	case model.WebhookFormatFeishu:
		return json.Marshal(map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": text},
		})
	default:
		return s.genericWebhookBody(notification, text)
	}
}

func (s *Service) genericWebhookBody(notification model.Notification, text string) ([]byte, error) {
	payload := webhookPayload{
		Version: 1,
		Event:   string(notification.Kind),
		Status:  string(notification.Status),
		Monitor: webhookMonitor{
			ID:     notification.MonitorID,
			Name:   notification.MonitorName,
			Type:   string(notification.MonitorType),
			Target: notification.MonitorTarget,
		},
		Message:    notification.Message,
		Text:       text,
		OccurredAt: notification.OccurredAt.UTC().Format(time.RFC3339),
	}
	details := make(map[string]any)
	if notification.Downtime > 0 {
		details["downtime_seconds"] = int(notification.Downtime.Seconds())
	}
	if notification.DowntimeCycles > 0 {
		details["downtime_cycles"] = notification.DowntimeCycles
	}
	if !notification.DowntimeStart.IsZero() {
		details["downtime_started_at"] = notification.DowntimeStart.UTC().Format(time.RFC3339)
	}
	if notification.Reminder > 0 {
		details["reminder"] = true
		details["reminder_number"] = notification.Reminder
		details["reminder_total"] = notification.ReminderTotal
	}
	if notification.Kind == model.EventWarnCert && notification.Cert != nil {
		details["certificate"] = map[string]any{
			"fingerprint":    notification.Cert.Fingerprint,
			"issuer":         notification.Cert.Issuer,
			"not_after":      notification.Cert.NotAfter.UTC().Format(time.RFC3339),
			"days_remaining": int(notification.Cert.NotAfter.Sub(notification.OccurredAt).Hours() / 24),
		}
	}
	if len(details) > 0 {
		payload.Details = details
	}
	return json.Marshal(payload)
}

func (s *Service) render(language string, notification model.Notification, locations ...*time.Location) string {
	location := time.UTC
	if len(locations) > 0 && locations[0] != nil {
		location = locations[0]
	}
	text := s.renderBase(language, location, notification)
	if notification.Reminder > 0 {
		translator := s.catalogs.Translator(language)
		return translator.Tf("notify.reminderPrefix", notification.Reminder, notification.ReminderTotal) + "\n" + text
	}
	return text
}

func (s *Service) renderBase(language string, location *time.Location, notification model.Notification) string {
	translator := s.catalogs.Translator(language)
	target := notification.MonitorTarget
	if notification.MonitorType == model.MonitorTypePush {
		target = translator.T("monitor.pushTarget")
	}
	switch notification.Kind {
	case model.EventDown:
		return translator.Tf("notify.down",
			notification.MonitorName,
			target,
			notification.Message,
			formatTime(notification.OccurredAt, location),
		)
	case model.EventUp:
		start := notification.DowntimeStart
		if start.IsZero() {
			start = notification.OccurredAt.Add(-notification.Downtime)
		}
		startText, endText := formatTimeRange(start, notification.OccurredAt, location)
		return translator.Tf("notify.up",
			notification.MonitorName,
			target,
			notification.DowntimeCycles,
			startText,
			endText,
		)
	case model.EventWarnCert:
		if notification.Cert == nil {
			return translator.Tf("notify.down", notification.MonitorName, target, notification.Message, formatTime(notification.OccurredAt, location))
		}
		days := int(notification.Cert.NotAfter.Sub(notification.OccurredAt).Hours() / 24)
		return translator.Tf("notify.warnCert",
			notification.MonitorName,
			target,
			days,
			formatTime(notification.Cert.NotAfter, location),
			notification.Cert.Issuer,
		)
	default:
		return notification.Message
	}
}

func (s *Service) httpClient(ctx context.Context, proxyID *int64) (*http.Client, func(), error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// "Direct" must mean the same thing for notifications as it does for
	// monitor checks; do not inherit HTTP_PROXY/HTTPS_PROXY from the environment.
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 64 << 10
	if proxyID != nil {
		proxyConfig, err := s.resolveProxy(ctx, *proxyID)
		if err != nil {
			return nil, func() {}, err
		}
		if err := outboundproxy.ApplyHTTPTransport(proxyConfig, transport); err != nil {
			return nil, func() {}, permanentError{err: fmt.Errorf("apply notification proxy: %w", err)}
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, transport.CloseIdleConnections, nil
}

func (s *Service) resolveProxy(ctx context.Context, proxyID int64) (*outboundproxy.Config, error) {
	stored, err := s.store.ProxyByID(ctx, proxyID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, permanentError{err: fmt.Errorf("notification proxy no longer exists: %w", err)}
	}
	if err != nil {
		return nil, fmt.Errorf("load notification proxy: %w", err)
	}
	password := ""
	if stored.PasswordEnc != "" {
		password, err = s.cipher.Decrypt(stored.PasswordEnc)
		if err != nil {
			return nil, permanentError{err: fmt.Errorf("decrypt notification proxy password: %w", err)}
		}
	}
	config, err := outboundproxy.NewConfig(stored.Type, stored.Address, stored.Username, password)
	if err != nil {
		return nil, permanentError{err: fmt.Errorf("invalid notification proxy configuration: %w", err)}
	}
	return config, nil
}

type permanentError struct {
	err error
}

func (e permanentError) Error() string {
	return e.err.Error()
}

// transportError keeps the underlying network or TLS cause while omitting the
// request URL, which can contain Telegram bot tokens or webhook query secrets.
func transportError(prefix string, err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return fmt.Errorf("%s: %w", prefix, urlErr.Err)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

type retryAfterError struct {
	wait time.Duration
}

func (e retryAfterError) Error() string {
	return fmt.Sprintf("retry after %s", e.wait)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

func (s *Service) retry(ctx context.Context, operation func() error) error {
	delays := s.retryDelays
	var last error
	for attempt := 0; attempt <= len(delays); attempt++ {
		if err := operation(); err == nil {
			return nil
		} else {
			last = err
			var permanent permanentError
			if errors.As(err, &permanent) {
				return err
			}
		}
		if attempt >= len(delays) {
			break
		}
		delay := delays[attempt]
		var retryAfter retryAfterError
		if errors.As(last, &retryAfter) && retryAfter.wait > 0 {
			delay = retryAfter.wait
			if delay > maxRetryAfter {
				delay = maxRetryAfter
			}
		}
		// A timed-out send may still have been delivered; logged so duplicate
		// deliveries can be correlated with retry attempts.
		s.log.Warn("notification send retry", "attempt", attempt+2, "delay", delay.String(), "error", last)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return last
}

func formatTime(value time.Time, location *time.Location) string {
	if value.IsZero() {
		return "—"
	}
	if location == nil {
		location = time.UTC
	}
	return value.In(location).Format("01-02 15:04:05")
}

// formatTimeRange keeps the date on the start and drops it from the end when
// both checks fall on the same local day.
func formatTimeRange(start, end time.Time, location *time.Location) (string, string) {
	if location == nil {
		location = time.UTC
	}
	startText := formatTime(start, location)
	endText := formatTime(end, location)
	if !start.IsZero() && !end.IsZero() &&
		start.In(location).Format("2006-01-02") == end.In(location).Format("2006-01-02") {
		endText = end.In(location).Format("15:04:05")
	}
	return startText, endText
}

func truncateError(value string) string {
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
