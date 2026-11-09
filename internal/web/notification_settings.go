package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

type notificationChannelFormValues struct {
	Name            string
	Type            model.NotificationChannelType
	Language        string
	TelegramChatIDs string
	TelegramToken   string
	WebhookURL      string
	WebhookFormat   model.WebhookFormat
	WebhookSecret   string
	Enabled         bool
	ProxyID         string
}

type notificationChannelRow struct {
	model.NotificationChannel
	TypeLabel     string
	LanguageLabel string
	Target        string
	ProxyName     string
	StatusLabel   string
	StatusClass   string
}

type notificationLogRow struct {
	Channel string
	Method  string
	Monitor string
	Reason  string
	Status  string
	Time    string
	Error   string
	Payload string
}

type notificationSettingsPage struct {
	settingsSectionPage
	Proxies                  []model.Proxy
	Channels                 []notificationChannelRow
	ChannelForm              notificationChannelFormValues
	ChannelFormOpen          bool
	ChannelFormEdit          bool
	ChannelFormAction        string
	ChannelSecretConfigured  bool
	ChannelWebhookConfigured bool
}

type logSettingsPage struct {
	settingsSectionPage
	NotificationLogs []notificationLogRow
}

var notificationStatusKeys = map[string]string{
	"pending": "notification.status.pending",
	"sent":    "notification.status.sent",
	"failed":  "notification.status.failed",
}

// notificationReasonKey derives the delivery reason from the notification
// dedupe key, whose prefix is the only persisted record of the triggering
// event kind. Reminder entries keep the reason of the state they repeat.
func notificationReasonKey(dedupeKey string) string {
	switch {
	case strings.HasPrefix(dedupeKey, "up:"):
		return "notification.reason.recovery"
	case strings.HasPrefix(dedupeKey, "down:"), strings.HasPrefix(dedupeKey, "remind_down:"):
		return "notification.reason.down"
	// Historical rows may still use warn_domain from the removed domain expiry
	// feature; keep them readable until retention expires.
	case strings.HasPrefix(dedupeKey, "warn_cert:"),
		strings.HasPrefix(dedupeKey, "warn_domain:"),
		strings.HasPrefix(dedupeKey, "remind_warning:"):
		return "notification.reason.warning"
	default:
		return ""
	}
}

func (s *Server) loadNotificationSettingsPage(r *http.Request, auth model.AuthInfo) (notificationSettingsPage, error) {
	base := s.newSettingsSectionPage(r, auth, "notifications")
	channels, err := s.store.ListNotificationChannels(r.Context())
	if err != nil {
		return notificationSettingsPage{}, err
	}
	proxies, err := s.store.ListProxies(r.Context())
	if err != nil {
		return notificationSettingsPage{}, err
	}
	proxyNames := make(map[int64]string, len(proxies))
	for _, proxy := range proxies {
		proxyNames[proxy.ID] = proxy.Name
	}
	channelRows := make([]notificationChannelRow, 0, len(channels))
	for _, channel := range channels {
		proxyName := ""
		if channel.ProxyID != nil {
			proxyName = proxyNames[*channel.ProxyID]
		}
		row := notificationChannelRow{
			NotificationChannel: channel,
			TypeLabel:           notificationChannelTypeLabel(base.I18n, channel.Type),
			LanguageLabel:       notificationLanguageLabel(base.I18n, channel.ResolvedLanguage()),
			Target:              channel.NotificationTarget(),
			ProxyName:           proxyName,
			StatusLabel:         base.I18n.T("notification.disabled"),
			StatusClass:         "status-paused",
		}
		if channel.Type == model.NotificationChannelWebhook && channel.ResolvedWebhookFormat() != model.WebhookFormatGeneric {
			row.TypeLabel += " · " + notificationWebhookFormatLabel(base.I18n, channel.ResolvedWebhookFormat())
		}
		if channel.Enabled {
			row.StatusLabel = base.I18n.T("notification.enabled")
			row.StatusClass = "status-up"
		}
		channelRows = append(channelRows, row)
	}
	return notificationSettingsPage{
		settingsSectionPage: base,
		Proxies:             proxies,
		Channels:            channelRows,
		ChannelForm: notificationChannelFormValues{
			Type:          model.NotificationChannelTelegram,
			Language:      model.NotificationLanguageZH,
			WebhookFormat: model.WebhookFormatGeneric,
			Enabled:       true,
		},
		ChannelFormAction: "/settings/notifications/channels",
	}, nil
}

func (s *Server) loadLogSettingsPage(r *http.Request, auth model.AuthInfo) (logSettingsPage, error) {
	base := s.newSettingsSectionPage(r, auth, "logs")
	logs, err := s.store.RecentNotifications(r.Context(), 100)
	if err != nil {
		return logSettingsPage{}, err
	}
	logRows := make([]notificationLogRow, 0, len(logs))
	for _, entry := range logs {
		status := entry.Status
		if key, ok := notificationStatusKeys[entry.Status]; ok {
			status = base.I18n.T(key)
		}
		reason := ""
		if key := notificationReasonKey(entry.DedupeKey); key != "" {
			reason = base.I18n.T(key)
		}
		payload := entry.Payload
		if entry.Channel == string(model.NotificationChannelWebhook) {
			payload = prettyNotificationPayload(payload)
		}
		logRows = append(logRows, notificationLogRow{
			Channel: entry.ChannelName,
			Method:  notificationChannelTypeLabel(base.I18n, model.NotificationChannelType(entry.Channel)),
			Monitor: entry.MonitorName,
			Reason:  reason,
			Status:  status,
			Time:    formatTime(base.I18n, entry.CreatedAt, base.Location),
			Error:   entry.Error,
			Payload: payload,
		})
	}
	return logSettingsPage{settingsSectionPage: base, NotificationLogs: logRows}, nil
}

func (s *Server) handleNotificationTest(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.notificationChannelNotFound")
		return
	}
	notice := "notification_channel_test_failed"
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.notifier.Test(ctx, id); err == nil {
		notice = "notification_channel_test_sent"
	} else {
		s.log.Warn("notification test failed", "channel_id", id, "error", err)
	}
	http.Redirect(w, r, "/settings/notifications?notice="+notice, http.StatusSeeOther)
}

func parseNotificationChannelForm(r *http.Request) notificationChannelFormValues {
	channelType := model.NotificationChannelType(r.PostFormValue("type"))
	if channelType != model.NotificationChannelTelegram && channelType != model.NotificationChannelWebhook {
		channelType = model.NotificationChannelTelegram
	}
	language := strings.TrimSpace(r.PostFormValue("language"))
	format := model.WebhookFormat(strings.TrimSpace(r.PostFormValue("webhook_format")))
	if format == "" {
		format = model.WebhookFormatGeneric
	}
	return notificationChannelFormValues{
		Name:            strings.TrimSpace(r.PostFormValue("name")),
		Type:            channelType,
		Language:        language,
		TelegramChatIDs: strings.TrimSpace(r.PostFormValue("telegram_chat_ids")),
		TelegramToken:   strings.TrimSpace(r.PostFormValue("telegram_token")),
		WebhookURL:      strings.TrimSpace(r.PostFormValue("webhook_url")),
		WebhookFormat:   format,
		WebhookSecret:   strings.TrimSpace(r.PostFormValue("webhook_secret")),
		Enabled:         r.PostFormValue("enabled") == "1",
		ProxyID:         strings.TrimSpace(r.PostFormValue("proxy_id")),
	}
}

func channelFormValues(channel model.NotificationChannel) notificationChannelFormValues {
	return notificationChannelFormValues{
		Name:            channel.Name,
		Type:            channel.Type,
		Language:        channel.ResolvedLanguage(),
		TelegramChatIDs: channel.TelegramChatIDs,
		// Webhook paths and query strings can carry bearer tokens. Never echo
		// the full URL; an empty value keeps the stored URL on update.
		WebhookURL:    "",
		WebhookFormat: channel.ResolvedWebhookFormat(),
		Enabled:       channel.Enabled,
		ProxyID:       channel.ProxyIDString(),
	}
}

func (s *Server) handleNotificationChannelCreate(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	values := parseNotificationChannelForm(r)
	channel, err := s.notificationChannelFromValues(r, values, nil)
	if err != nil {
		s.renderChannelSettingsError(w, r, auth, values, 0, formErrorKey(err))
		return
	}
	existingChannels, err := s.store.ListNotificationChannels(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	createdID, err := s.store.CreateNotificationChannel(r.Context(), channel, s.now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrNotificationChannelNameTaken) {
			s.renderChannelSettingsError(w, r, auth, values, 0, "error.notificationChannelNameTaken")
			return
		}
		s.log.Error("create notification channel", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	// The first channel adopts monitors that still have no explicit selection,
	// preserving the previous global-channel behavior for upgraded installs.
	if len(existingChannels) == 0 {
		linked, linkErr := s.store.LinkNotificationChannelToUnlinkedMonitors(r.Context(), createdID)
		if linkErr != nil {
			s.log.Warn("link new notification channel to existing monitors", "channel_id", createdID, "error", linkErr)
		}
		for _, monitorID := range linked {
			if err := s.scheduler.Reload(monitorID); err != nil {
				s.log.Warn("reload scheduler after notification channel create", "monitor_id", monitorID, "error", err)
			}
		}
	}
	http.Redirect(w, r, "/settings/notifications?notice=notification_channel_created", http.StatusSeeOther)
}

func (s *Server) handleNotificationChannelUpdate(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.notificationChannelNotFound")
		return
	}
	if !s.authorizedPost(w, r, auth) {
		return
	}
	existing, err := s.store.NotificationChannelByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "error.notificationChannelNotFound")
		return
	}
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	values := parseNotificationChannelForm(r)
	channel, err := s.notificationChannelFromValues(r, values, &existing)
	if err != nil {
		s.renderChannelSettingsError(w, r, auth, values, id, formErrorKey(err))
		return
	}
	if err := s.store.UpdateNotificationChannel(r.Context(), channel, s.now().UTC()); err != nil {
		switch {
		case errors.Is(err, store.ErrNotificationChannelNameTaken):
			s.renderChannelSettingsError(w, r, auth, values, id, "error.notificationChannelNameTaken")
		case errors.Is(err, store.ErrNotFound):
			s.renderError(w, r, http.StatusNotFound, "error.notificationChannelNotFound")
		default:
			s.log.Error("update notification channel", "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		}
		return
	}
	http.Redirect(w, r, "/settings/notifications?notice=notification_channel_updated", http.StatusSeeOther)
}

func (s *Server) handleNotificationChannelDelete(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.notificationChannelNotFound")
		return
	}
	if !s.authorizedPost(w, r, auth) {
		return
	}
	monitorIDs, err := s.store.MonitorIDsForNotificationChannel(r.Context(), id)
	if err != nil {
		s.log.Error("list monitors for notification channel", "channel_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if err := s.store.DeleteNotificationChannel(r.Context(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("delete notification channel", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	for _, monitorID := range monitorIDs {
		if err := s.scheduler.Reload(monitorID); err != nil {
			s.log.Warn("reload scheduler after notification channel delete", "monitor_id", monitorID, "error", err)
		}
	}
	http.Redirect(w, r, "/settings/notifications?notice=notification_channel_deleted", http.StatusSeeOther)
}

func (s *Server) notificationChannelFromValues(r *http.Request, values notificationChannelFormValues, existing *model.NotificationChannel) (model.NotificationChannel, error) {
	channel := model.NotificationChannel{
		Name:            values.Name,
		Type:            values.Type,
		Language:        values.Language,
		Enabled:         values.Enabled,
		TelegramChatIDs: values.TelegramChatIDs,
		WebhookURL:      values.WebhookURL,
		WebhookFormat:   values.WebhookFormat,
	}
	if existing != nil {
		channel.ID = existing.ID
		channel.CreatedAt = existing.CreatedAt
		if existing.Type == values.Type {
			if channel.Type == model.NotificationChannelTelegram ||
				existing.ResolvedWebhookFormat() == channel.ResolvedWebhookFormat() {
				channel.SecretEnc = existing.SecretEnc
			}
			if channel.Type == model.NotificationChannelWebhook && strings.TrimSpace(channel.WebhookURL) == "" {
				// The edit form intentionally does not echo the URL because its
				// path/query may contain a bearer token.
				channel.WebhookURL = existing.WebhookURL
			}
		}
	}
	if channel.Type == model.NotificationChannelTelegram {
		if values.TelegramToken != "" {
			encrypted, err := s.cipher.Encrypt(values.TelegramToken)
			if err != nil {
				return model.NotificationChannel{}, err
			}
			channel.SecretEnc = encrypted
		}
		if channel.SecretEnc == "" {
			return model.NotificationChannel{}, newFormError("error.notificationChannelToken")
		}
	} else if channel.ResolvedWebhookFormat() == model.WebhookFormatWeCom {
		channel.SecretEnc = ""
	} else if values.WebhookSecret != "" {
		encrypted, err := s.cipher.Encrypt(values.WebhookSecret)
		if err != nil {
			return model.NotificationChannel{}, err
		}
		channel.SecretEnc = encrypted
	}
	if values.ProxyID != "" {
		proxyID, err := strconv.ParseInt(values.ProxyID, 10, 64)
		if err != nil || proxyID <= 0 {
			return model.NotificationChannel{}, newFormError("error.proxyNotFound")
		}
		if _, err := s.store.ProxyByID(r.Context(), proxyID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return model.NotificationChannel{}, newFormError("error.proxyNotFound")
			}
			return model.NotificationChannel{}, err
		}
		channel.ProxyID = &proxyID
	}
	if err := channel.Validate(); err != nil {
		return model.NotificationChannel{}, newFormError(channelValidationKey(err))
	}
	return channel, nil
}

func channelValidationKey(err error) string {
	switch {
	case errors.Is(err, model.ErrChannelName):
		return "error.notificationChannelName"
	case errors.Is(err, model.ErrChannelType):
		return "error.notificationChannelType"
	case errors.Is(err, model.ErrChannelLanguage):
		return "error.notificationChannelLanguage"
	case errors.Is(err, model.ErrChannelChatIDs):
		return "error.notificationChannelChatIDs"
	case errors.Is(err, model.ErrChannelWebhookURL):
		return "error.webhookURL"
	case errors.Is(err, model.ErrChannelWebhookFormat):
		return "error.notificationChannelWebhookFormat"
	default:
		return "error.invalidRequest"
	}
}

func (s *Server) renderChannelSettingsError(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, values notificationChannelFormValues, editingID int64, errorKey string) {
	page, err := s.loadNotificationSettingsPage(r, auth)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.ChannelFormOpen = true
	// Never put a webhook URL back into HTML after a validation error; its
	// path or query may contain a bearer token.
	values.WebhookURL = ""
	page.ChannelForm = values
	page.ChannelFormEdit = editingID > 0
	if editingID > 0 {
		page.ChannelFormAction = "/settings/notifications/channels/" + strconv.FormatInt(editingID, 10)
		if existing, err := s.store.NotificationChannelByID(r.Context(), editingID); err == nil {
			page.ChannelSecretConfigured = existing.SecretEnc != ""
			page.ChannelWebhookConfigured = existing.WebhookURL != ""
		}
	}
	page.Error = page.I18n.T(errorKey)
	s.render(w, "settings_notifications", http.StatusBadRequest, page)
}

func notificationChannelTypeLabel(translator interface{ T(string) string }, channelType model.NotificationChannelType) string {
	switch channelType {
	case model.NotificationChannelTelegram:
		return translator.T("notification.telegram")
	case model.NotificationChannelWebhook:
		return translator.T("notification.webhook")
	default:
		return string(channelType)
	}
}

func notificationWebhookFormatLabel(translator interface{ T(string) string }, format model.WebhookFormat) string {
	switch format {
	case model.WebhookFormatDingTalk:
		return translator.T("notification.webhookFormatDingTalk")
	case model.WebhookFormatWeCom:
		return translator.T("notification.webhookFormatWeCom")
	case model.WebhookFormatFeishu:
		return translator.T("notification.webhookFormatFeishu")
	default:
		return translator.T("notification.webhookFormatGeneric")
	}
}

func notificationLanguageLabel(translator interface{ T(string) string }, language string) string {
	if language == model.NotificationLanguageEN {
		return translator.T("language.en")
	}
	return translator.T("language.zhCN")
}

func prettyNotificationPayload(payload string) string {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return payload
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, []byte(trimmed), "", "  "); err != nil {
		return payload
	}
	return formatted.String()
}
