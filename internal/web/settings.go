package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

type settingsSectionPage struct {
	pageData
	SectionTitle string
	SectionIntro string
}

type generalSettingsPage struct {
	settingsSectionPage
	Timezone        string
	TimezoneOptions []timezoneOption
}

type publicSettingsPage struct {
	settingsSectionPage
	PublicStatus        publicStatusFormValues
	PublicStatusURL     string
	PublicMonitorGroups []publicMonitorGroup
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
	section := settingsSectionForPath(r.URL.Path)
	if editValue := strings.TrimSpace(r.URL.Query().Get("edit")); editValue != "" && section != "proxies" {
		http.Redirect(w, r, "/settings/proxies?edit="+url.QueryEscape(editValue), http.StatusSeeOther)
		return
	}
	if channelValue := strings.TrimSpace(r.URL.Query().Get("channel")); channelValue != "" && section != "notifications" {
		http.Redirect(w, r, "/settings/notifications?channel="+url.QueryEscape(channelValue), http.StatusSeeOther)
		return
	}

	switch section {
	case "public":
		page, err := s.loadPublicSettingsPage(r, auth)
		if err != nil {
			s.log.Error("load public settings", "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		s.render(w, "settings_public", http.StatusOK, page)
	case "notifications":
		page, err := s.loadNotificationSettingsPage(r, auth)
		if err != nil {
			s.log.Error("load notification settings", "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		if channelValue := strings.TrimSpace(r.URL.Query().Get("channel")); channelValue != "" {
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
			page.ChannelWebhookConfigured = channel.WebhookURL != ""
			page.ChannelForm = channelFormValues(channel)
		}
		s.render(w, "settings_notifications", http.StatusOK, page)
	case "logs":
		page, err := s.loadLogSettingsPage(r, auth)
		if err != nil {
			s.log.Error("load notification log settings", "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		s.render(w, "settings_logs", http.StatusOK, page)
	case "proxies":
		page, err := s.loadProxySettingsPage(r, auth)
		if err != nil {
			s.log.Error("load proxy settings", "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		if editValue := strings.TrimSpace(r.URL.Query().Get("edit")); editValue != "" {
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
		s.render(w, "settings_proxies", http.StatusOK, page)
	default:
		page, err := s.loadGeneralSettingsPage(r, auth)
		if err != nil {
			s.log.Error("load general settings", "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		s.render(w, "settings_general", http.StatusOK, page)
	}
}

func (s *Server) newSettingsSectionPage(r *http.Request, auth model.AuthInfo, section string) settingsSectionPage {
	titleKey, introKey := settingsSectionLocaleKeys(section)
	data := s.authenticatedPage(r, auth, titleKey)
	data.ActiveNav = "settings"
	return settingsSectionPage{
		pageData:     data,
		SectionTitle: data.I18n.T(titleKey),
		SectionIntro: data.I18n.T(introKey),
	}
}

func (s *Server) loadGeneralSettingsPage(r *http.Request, auth model.AuthInfo) (generalSettingsPage, error) {
	base := s.newSettingsSectionPage(r, auth, "general")
	allSettings, err := s.settings(r.Context())
	if err != nil {
		return generalSettingsPage{}, err
	}
	timezone := strings.TrimSpace(allSettings[model.SettingTimezone])
	if timezone == "" {
		timezone = model.DefaultTimezone
	}
	return generalSettingsPage{
		settingsSectionPage: base,
		Timezone:            timezone,
		TimezoneOptions:     buildTimezoneOptions(timezone, s.now()),
	}, nil
}

func (s *Server) loadPublicSettingsPage(r *http.Request, auth model.AuthInfo) (publicSettingsPage, error) {
	base := s.newSettingsSectionPage(r, auth, "public")
	allSettings, err := s.settings(r.Context())
	if err != nil {
		return publicSettingsPage{}, err
	}
	publicStatus := publicStatusFromSettings(allSettings)
	monitors, err := s.store.ListMonitors(r.Context())
	if err != nil {
		return publicSettingsPage{}, err
	}
	selected := make(map[int64]struct{}, len(publicStatus.MonitorIDs))
	for _, id := range publicStatus.MonitorIDs {
		selected[id] = struct{}{}
	}
	options := make([]publicMonitorOption, 0, len(monitors))
	for _, monitor := range monitors {
		_, isSelected := selected[monitor.ID]
		options = append(options, publicMonitorOption{
			ID:       monitor.ID,
			Name:     monitor.Name,
			Type:     monitor.Type,
			Selected: isSelected,
		})
	}
	return publicSettingsPage{
		settingsSectionPage: base,
		PublicStatus:        publicStatus,
		PublicStatusURL:     s.requestScheme(r) + "://" + s.requestHost(r) + "/status",
		PublicMonitorGroups: groupPublicMonitorOptions(base.I18n, options),
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
		page, loadErr := s.loadGeneralSettingsPage(r, auth)
		if loadErr != nil {
			s.log.Error("load settings for timezone error", "error", loadErr)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		page.Timezone = timezone
		page.Error = page.I18n.T("error.timezone")
		s.render(w, "settings_general", http.StatusBadRequest, page)
		return
	}
	if err := s.store.SetSettings(r.Context(), map[string]string{
		model.SettingTimezone: timezone,
	}, s.now().UTC()); err != nil {
		s.log.Error("save timezone", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	s.invalidateSettings()
	http.Redirect(w, r, "/settings/general?notice=timezone_saved", http.StatusSeeOther)
}
