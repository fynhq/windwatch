package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	outboundproxy "github.com/fynhq/windwatch/internal/proxy"
	"github.com/fynhq/windwatch/internal/store"
)

const proxyTestTarget = "example.com:443"

type proxyFormValues struct {
	Name     string
	Type     model.ProxyType
	Address  string
	Username string
	Password string
}

type notificationChannelFormValues struct {
	Name            string
	Type            model.NotificationChannelType
	Language        string
	TelegramChatIDs string
	TelegramToken   string
	WebhookURL      string
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
	Target  string
	Status  string
	Time    string
	Error   string
	Payload string
}

var notificationStatusKeys = map[string]string{
	"pending": "notification.status.pending",
	"sent":    "notification.status.sent",
	"failed":  "notification.status.failed",
}

type settingsPage struct {
	pageData
	SectionTitle     string
	SectionIntro     string
	Proxies          []model.Proxy
	ProxyForm        proxyFormValues
	ProxyFormOpen    bool
	ProxyFormEdit    bool
	ProxyFormAction  string
	ProxyTestAction  string
	ProxyTestDetail  string
	ProxyTypeOptions []model.ProxyType

	Channels                []notificationChannelRow
	ChannelForm             notificationChannelFormValues
	ChannelFormOpen         bool
	ChannelFormEdit         bool
	ChannelFormAction       string
	ChannelSecretConfigured bool

	NotificationLogs []notificationLogRow

	PublicStatus    publicStatusFormValues
	PublicStatusURL string
	PublicMonitors  []publicMonitorOption
	Timezone        string
	TimezoneOptions []timezoneOption
}

type settingsIndexItem struct {
	Title string
	Intro string
	Href  string
	Icon  string
}

type settingsIndexPage struct {
	pageData
	Items []settingsIndexItem
}

func (s *Server) handleSettingsIndex(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	data := s.authenticatedPage(r, auth, "settings.overviewTitle")
	data.ActiveNav = "settings"
	data.SettingsSection = "overview"
	data.BackURL = "/"
	translator := data.I18n
	page := settingsIndexPage{
		pageData: data,
		Items: []settingsIndexItem{
			{Title: translator.T("settings.navGeneral"), Intro: translator.T("settings.systemIntro"), Href: "/settings/general", Icon: "general"},
			{Title: translator.T("settings.navPublic"), Intro: translator.T("settings.publicStatusIntro"), Href: "/settings/public", Icon: "public"},
			{Title: translator.T("settings.navNotifications"), Intro: translator.T("notification.intro"), Href: "/settings/notifications", Icon: "notifications"},
			{Title: translator.T("settings.navLogs"), Intro: translator.T("settings.logsIntro"), Href: "/settings/logs", Icon: "logs"},
			{Title: translator.T("settings.navProxies"), Intro: translator.T("proxy.listHint"), Href: "/settings/proxies", Icon: "proxies"},
		},
	}
	s.render(w, "settings_index", http.StatusOK, page)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	page, err := s.loadSettingsPage(r, auth)
	if err != nil {
		s.log.Error("load settings", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}

	if editValue := strings.TrimSpace(r.URL.Query().Get("edit")); editValue != "" {
		if page.SettingsSection != "proxies" {
			http.Redirect(w, r, "/settings/proxies?edit="+url.QueryEscape(editValue), http.StatusSeeOther)
			return
		}
		id, err := strconv.ParseInt(editValue, 10, 64)
		if err != nil || id <= 0 {
			s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
			return
		}
		proxy, err := s.store.ProxyByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
			return
		}
		if err != nil {
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		page.ProxyFormOpen = true
		page.ProxyFormEdit = true
		page.ProxyFormAction = "/settings/proxies/" + strconv.FormatInt(proxy.ID, 10)
		page.ProxyTestAction = page.ProxyFormAction + "/test"
		page.ProxyForm = proxyFormValues{
			Name:     proxy.Name,
			Type:     proxy.Type,
			Address:  proxy.Address,
			Username: proxy.Username,
		}
	}

	if channelValue := strings.TrimSpace(r.URL.Query().Get("channel")); channelValue != "" {
		if page.SettingsSection != "notifications" {
			http.Redirect(w, r, "/settings/notifications?channel="+url.QueryEscape(channelValue), http.StatusSeeOther)
			return
		}
		id, err := strconv.ParseInt(channelValue, 10, 64)
		if err != nil || id <= 0 {
			s.renderError(w, r, http.StatusNotFound, "error.notificationChannelNotFound")
			return
		}
		channel, err := s.store.NotificationChannelByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, r, http.StatusNotFound, "error.notificationChannelNotFound")
			return
		}
		if err != nil {
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		page.ChannelFormOpen = true
		page.ChannelFormEdit = true
		page.ChannelFormAction = "/settings/notifications/channels/" + strconv.FormatInt(channel.ID, 10)
		page.ChannelSecretConfigured = channel.SecretEnc != ""
		page.ChannelForm = channelFormValues(channel)
	}

	s.render(w, "settings", http.StatusOK, page)
}

func (s *Server) loadSettingsPage(r *http.Request, auth model.AuthInfo) (settingsPage, error) {
	proxies, err := s.store.ListProxies(r.Context())
	if err != nil {
		return settingsPage{}, err
	}
	channels, err := s.store.ListNotificationChannels(r.Context())
	if err != nil {
		return settingsPage{}, err
	}
	logs, err := s.store.RecentNotifications(r.Context(), 100)
	if err != nil {
		return settingsPage{}, err
	}
	allSettings, err := s.store.Settings(r.Context())
	if err != nil {
		return settingsPage{}, err
	}
	publicStatus := publicStatusFromSettings(allSettings)
	timezone := strings.TrimSpace(allSettings[model.SettingTimezone])
	if timezone == "" {
		timezone = model.DefaultTimezone
	}
	titleKey, introKey := settingsSectionLocaleKeys(settingsSectionForPath(r.URL.Path))
	data := s.authenticatedPage(r, auth, titleKey)
	data.ActiveNav = "settings"
	publicSelected := make(map[int64]struct{}, len(publicStatus.MonitorIDs))
	for _, id := range publicStatus.MonitorIDs {
		publicSelected[id] = struct{}{}
	}
	publicMonitors, err := s.store.ListMonitors(r.Context())
	if err != nil {
		return settingsPage{}, err
	}
	publicOptions := make([]publicMonitorOption, 0, len(publicMonitors))
	for _, monitor := range publicMonitors {
		_, selected := publicSelected[monitor.ID]
		publicOptions = append(publicOptions, publicMonitorOption{
			ID:        monitor.ID,
			Name:      monitor.Name,
			TypeLabel: s.typeLabel(data.I18n, monitor.Type),
			Selected:  selected,
		})
	}
	logRows := make([]notificationLogRow, 0, len(logs))
	for _, entry := range logs {
		status := entry.Status
		if key, ok := notificationStatusKeys[entry.Status]; ok {
			status = data.I18n.T(key)
		}
		payload := entry.Payload
		if entry.Channel == string(model.NotificationChannelWebhook) {
			payload = prettyNotificationPayload(payload)
		}
		logRows = append(logRows, notificationLogRow{
			Channel: entry.ChannelName,
			Method:  notificationChannelTypeLabel(data.I18n, model.NotificationChannelType(entry.Channel)),
			Target:  entry.Target,
			Status:  status,
			Time:    formatTime(data.I18n, entry.CreatedAt, data.Location),
			Error:   entry.Error,
			Payload: payload,
		})
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
			TypeLabel:           notificationChannelTypeLabel(data.I18n, channel.Type),
			LanguageLabel:       notificationLanguageLabel(data.I18n, channel.ResolvedLanguage()),
			Target:              channel.NotificationTarget(),
			ProxyName:           proxyName,
			StatusLabel:         data.I18n.T("notification.disabled"),
			StatusClass:         "status-paused",
		}
		if channel.Enabled {
			row.StatusLabel = data.I18n.T("notification.enabled")
			row.StatusClass = "status-up"
		}
		channelRows = append(channelRows, row)
	}
	return settingsPage{
		pageData:         data,
		SectionTitle:     data.I18n.T(titleKey),
		SectionIntro:     data.I18n.T(introKey),
		Proxies:          proxies,
		ProxyFormAction:  "/settings/proxies",
		ProxyTestAction:  "/settings/proxies/test",
		ProxyTypeOptions: []model.ProxyType{model.ProxyHTTP, model.ProxySOCKS5},
		Channels:         channelRows,
		ChannelForm: notificationChannelFormValues{
			Type:     model.NotificationChannelTelegram,
			Language: model.NotificationLanguageZH,
			Enabled:  true,
		},
		ChannelFormAction: "/settings/notifications/channels",
		NotificationLogs:  logRows,
		PublicStatus:      publicStatus,
		PublicStatusURL:   s.requestScheme(r) + "://" + s.requestHost(r) + "/status",
		PublicMonitors:    publicOptions,
		Timezone:          timezone,
		TimezoneOptions:   buildTimezoneOptions(timezone, s.now()),
	}, nil
}

func settingsSectionLocaleKeys(section string) (titleKey, introKey string) {
	switch section {
	case "public":
		return "settings.navPublic", "settings.publicStatusIntro"
	case "notifications":
		return "settings.navNotifications", "notification.intro"
	case "logs":
		return "settings.navLogs", "settings.logsIntro"
	case "proxies":
		return "settings.navProxies", "proxy.listHint"
	default:
		return "settings.navGeneral", "settings.systemIntro"
	}
}

func (s *Server) handleTimezoneSave(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	timezone := strings.TrimSpace(r.PostFormValue("timezone"))
	if timezone == "" {
		timezone = model.DefaultTimezone
	}
	if _, err := model.ValidateTimezone(timezone); err != nil {
		page, loadErr := s.loadSettingsPage(r, auth)
		if loadErr != nil {
			s.log.Error("load settings for timezone error", "error", loadErr)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		page.Timezone = timezone
		page.Error = page.I18n.T("error.timezone")
		s.render(w, "settings", http.StatusBadRequest, page)
		return
	}
	if err := s.store.SetSettings(r.Context(), map[string]string{
		model.SettingTimezone: timezone,
	}, s.now().UTC()); err != nil {
		s.log.Error("save timezone", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	http.Redirect(w, r, "/settings/general?notice=timezone_saved", http.StatusSeeOther)
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
	if err := s.notifier.Test(r.Context(), id); err == nil {
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
	return notificationChannelFormValues{
		Name:            strings.TrimSpace(r.PostFormValue("name")),
		Type:            channelType,
		Language:        language,
		TelegramChatIDs: strings.TrimSpace(r.PostFormValue("telegram_chat_ids")),
		TelegramToken:   strings.TrimSpace(r.PostFormValue("telegram_token")),
		WebhookURL:      strings.TrimSpace(r.PostFormValue("webhook_url")),
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
		WebhookURL:      channel.WebhookURL,
		Enabled:         channel.Enabled,
		ProxyID:         channel.ProxyIDString(),
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
	}
	if existing != nil {
		channel.ID = existing.ID
		channel.CreatedAt = existing.CreatedAt
		if existing.Type == values.Type {
			channel.SecretEnc = existing.SecretEnc
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
	} else {
		if values.WebhookSecret != "" {
			encrypted, err := s.cipher.Encrypt(values.WebhookSecret)
			if err != nil {
				return model.NotificationChannel{}, err
			}
			channel.SecretEnc = encrypted
		}
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
	default:
		return "error.invalidRequest"
	}
}

func (s *Server) renderChannelSettingsError(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, values notificationChannelFormValues, editingID int64, errorKey string) {
	page, err := s.loadSettingsPage(r, auth)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.ChannelFormOpen = true
	page.ChannelForm = values
	page.ChannelFormEdit = editingID > 0
	if editingID > 0 {
		page.ChannelFormAction = "/settings/notifications/channels/" + strconv.FormatInt(editingID, 10)
		if existing, err := s.store.NotificationChannelByID(r.Context(), editingID); err == nil {
			page.ChannelSecretConfigured = existing.SecretEnc != ""
		}
	}
	page.Error = page.I18n.T(errorKey)
	s.render(w, "settings", http.StatusBadRequest, page)
}

func (s *Server) handleProxyTest(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	var existing *model.Proxy
	if idValue := r.PathValue("id"); idValue != "" {
		id, err := strconv.ParseInt(idValue, 10, 64)
		if err != nil || id <= 0 {
			s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
			return
		}
		proxy, err := s.store.ProxyByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
			return
		}
		if err != nil {
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		existing = &proxy
	}

	values := parseProxyForm(r)
	password := values.Password
	if password == "" && existing != nil && existing.PasswordEnc != "" {
		decrypted, err := s.cipher.Decrypt(existing.PasswordEnc)
		if err != nil {
			s.log.Error("decrypt proxy password for test", "proxy_id", existing.ID, "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		password = decrypted
	}
	config := &outboundproxy.Config{
		Type:     values.Type,
		Address:  strings.TrimSpace(values.Address),
		Username: values.Username,
		Password: password,
	}
	if err := config.Validate(); err != nil {
		key := "error.proxyAddress"
		if values.Type != model.ProxyHTTP && values.Type != model.ProxySOCKS5 {
			key = "error.proxyType"
		}
		s.renderProxyTestError(w, r, auth, values, existing, key, "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := outboundproxy.CheckConnectivity(ctx, config, proxyTestTarget, 8*time.Second); err != nil {
		s.log.Warn("proxy connectivity test failed", "address", config.Address, "error", err)
		s.renderProxyTestError(w, r, auth, values, existing, "error.proxyTestFailed", truncateDetail(err.Error()))
		return
	}

	s.renderProxyTestSuccess(w, r, auth, values, existing)
}

func (s *Server) renderProxyTestSuccess(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, values proxyFormValues, existing *model.Proxy) {
	page, err := s.loadSettingsPage(r, auth)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.ProxyFormOpen = true
	page.ProxyForm = values
	page.Notice = page.I18n.T("notice.proxyTestOK")
	if existing != nil {
		page.ProxyFormEdit = true
		page.ProxyFormAction = "/settings/proxies/" + strconv.FormatInt(existing.ID, 10)
		page.ProxyTestAction = page.ProxyFormAction + "/test"
	}
	s.render(w, "settings", http.StatusOK, page)
}

func (s *Server) renderProxyTestError(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, values proxyFormValues, existing *model.Proxy, errorKey, detail string) {
	page, err := s.loadSettingsPage(r, auth)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.ProxyFormOpen = true
	page.ProxyForm = values
	page.Error = page.I18n.T(errorKey)
	page.ProxyTestDetail = detail
	if existing != nil {
		page.ProxyFormEdit = true
		page.ProxyFormAction = "/settings/proxies/" + strconv.FormatInt(existing.ID, 10)
		page.ProxyTestAction = page.ProxyFormAction + "/test"
	}
	s.render(w, "settings", http.StatusBadRequest, page)
}

func truncateDetail(value string) string {
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

func (s *Server) handleProxyCreate(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	values := parseProxyForm(r)
	proxy, err := s.proxyFromValues(r, values, nil)
	if err != nil {
		s.renderSettingsError(w, r, auth, values, 0, formErrorKey(err))
		return
	}
	if _, err := s.store.CreateProxy(r.Context(), proxy, s.now().UTC()); err != nil {
		if errors.Is(err, store.ErrProxyNameTaken) {
			s.renderSettingsError(w, r, auth, values, 0, "error.proxyNameTaken")
			return
		}
		s.log.Error("create proxy", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	http.Redirect(w, r, "/settings/proxies?notice=proxy_created", http.StatusSeeOther)
}

func (s *Server) handleProxyUpdate(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
		return
	}
	if !s.authorizedPost(w, r, auth) {
		return
	}
	existing, err := s.store.ProxyByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
		return
	}
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	values := parseProxyForm(r)
	proxy, err := s.proxyFromValues(r, values, &existing)
	if err != nil {
		s.renderSettingsError(w, r, auth, values, id, formErrorKey(err))
		return
	}
	if err := s.store.UpdateProxy(r.Context(), proxy, s.now().UTC()); err != nil {
		if errors.Is(err, store.ErrProxyNameTaken) {
			s.renderSettingsError(w, r, auth, values, id, "error.proxyNameTaken")
			return
		}
		s.log.Error("update proxy", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	http.Redirect(w, r, "/settings/proxies?notice=proxy_updated", http.StatusSeeOther)
}

func (s *Server) handleProxyDelete(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
		return
	}
	if !s.authorizedPost(w, r, auth) {
		return
	}
	if err := s.store.DeleteProxy(r.Context(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("delete proxy", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	http.Redirect(w, r, "/settings/proxies?notice=proxy_deleted", http.StatusSeeOther)
}

func (s *Server) proxyFromValues(r *http.Request, values proxyFormValues, existing *model.Proxy) (model.Proxy, error) {
	proxy := model.Proxy{
		Name:     strings.TrimSpace(values.Name),
		Type:     values.Type,
		Address:  strings.TrimSpace(values.Address),
		Username: values.Username,
	}
	if existing != nil {
		proxy.ID = existing.ID
		proxy.CreatedAt = existing.CreatedAt
		proxy.PasswordEnc = existing.PasswordEnc
	}
	if values.Password != "" {
		encrypted, err := s.cipher.Encrypt(values.Password)
		if err != nil {
			return model.Proxy{}, err
		}
		proxy.PasswordEnc = encrypted
	}
	if err := proxy.Validate(); err != nil {
		return model.Proxy{}, newFormError(proxyValidationKey(err))
	}
	return proxy, nil
}

func parseProxyForm(r *http.Request) proxyFormValues {
	return proxyFormValues{
		Name:     strings.TrimSpace(r.PostFormValue("name")),
		Type:     model.ProxyType(r.PostFormValue("type")),
		Address:  strings.TrimSpace(r.PostFormValue("address")),
		Username: strings.TrimSpace(r.PostFormValue("username")),
		Password: r.PostFormValue("password"),
	}
}

func proxyValidationKey(err error) string {
	switch {
	case errors.Is(err, model.ErrProxyName):
		return "error.proxyName"
	case errors.Is(err, model.ErrProxyType):
		return "error.proxyType"
	case errors.Is(err, model.ErrProxyAddress):
		return "error.proxyAddress"
	default:
		return "error.invalidRequest"
	}
}

func (s *Server) renderSettingsError(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, values proxyFormValues, editingID int64, errorKey string) {
	page, err := s.loadSettingsPage(r, auth)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.ProxyFormOpen = true
	page.ProxyFormEdit = editingID > 0
	page.ProxyForm = values
	if editingID > 0 {
		page.ProxyFormAction = "/settings/proxies/" + strconv.FormatInt(editingID, 10)
		page.ProxyTestAction = page.ProxyFormAction + "/test"
	}
	page.Error = page.I18n.T(errorKey)
	s.render(w, "settings", http.StatusBadRequest, page)
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
