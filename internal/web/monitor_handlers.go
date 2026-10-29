package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/domains"
	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

type formError struct {
	key string
}

func (e *formError) Error() string {
	return e.key
}

func newFormError(key string) error {
	return &formError{key: key}
}

func formErrorKey(err error) string {
	var fieldError *formError
	if errors.As(err, &fieldError) {
		return fieldError.key
	}
	switch {
	case errors.Is(err, model.ErrMonitorName):
		return "error.monitorName"
	case errors.Is(err, model.ErrMonitorType):
		return "error.invalidRequest"
	case errors.Is(err, model.ErrInterval):
		return "error.interval"
	case errors.Is(err, model.ErrTimeout):
		return "error.timeout"
	case errors.Is(err, model.ErrThreshold):
		return "error.threshold"
	case errors.Is(err, model.ErrReminderCount):
		return "error.reminderCount"
	case errors.Is(err, model.ErrReminderInterval):
		return "error.reminderInterval"
	case errors.Is(err, model.ErrHTTPURL):
		return "error.url"
	case errors.Is(err, model.ErrHTTPMethod):
		return "error.method"
	case errors.Is(err, model.ErrHTTPStatus):
		return "error.expectedStatus"
	case errors.Is(err, model.ErrHTTPKeyword):
		return "error.keyword"
	case errors.Is(err, model.ErrTCPHost):
		return "error.host"
	case errors.Is(err, model.ErrTCPPort):
		return "error.port"
	case errors.Is(err, model.ErrDockerContainer):
		return "error.dockerContainer"
	case errors.Is(err, model.ErrHTTPSCert):
		return "error.certCheck"
	case errors.Is(err, model.ErrDomainCheck):
		return "error.domainCheck"
	default:
		return "error.invalidRequest"
	}
}

func parseFormValues(r *http.Request) monitorFormValues {
	values := monitorFormValues{
		Name:                   strings.TrimSpace(r.PostFormValue("name")),
		Interval:               strings.TrimSpace(r.PostFormValue("interval")),
		Timeout:                strings.TrimSpace(r.PostFormValue("timeout")),
		FailThreshold:          strings.TrimSpace(r.PostFormValue("fail_threshold")),
		RecoverThreshold:       strings.TrimSpace(r.PostFormValue("recover_threshold")),
		ProxyID:                strings.TrimSpace(r.PostFormValue("proxy_id")),
		ChannelIDs:             r.PostForm["notification_channel_ids"],
		WarningReminderCount:   strings.TrimSpace(r.PostFormValue("warning_reminder_count")),
		WarningReminderMinutes: strings.TrimSpace(r.PostFormValue("warning_reminder_interval_minutes")),
		FailureReminderCount:   strings.TrimSpace(r.PostFormValue("failure_reminder_count")),
		FailureReminderMinutes: strings.TrimSpace(r.PostFormValue("failure_reminder_interval_minutes")),
		URL:                    strings.TrimSpace(r.PostFormValue("url")),
		Method:                 strings.TrimSpace(r.PostFormValue("method")),
		ExpectedStatus:         strings.TrimSpace(r.PostFormValue("expected_status")),
		Keyword:                r.PostFormValue("keyword"),
		FollowRedirects:        r.PostFormValue("follow_redirects") != "",
		TLSSkipVerify:          r.PostFormValue("tls_skip_verify") != "",
		CertCheck:              r.PostFormValue("cert_check") != "",
		CertWarnDays:           strings.TrimSpace(r.PostFormValue("cert_warn_days")),
		DomainCheck:            r.PostFormValue("domain_check") != "",
		DomainWarnDays:         strings.TrimSpace(r.PostFormValue("domain_warn_days")),
		Host:                   strings.TrimSpace(r.PostFormValue("host")),
		Port:                   strings.TrimSpace(r.PostFormValue("port")),
		DockerContainer:        strings.TrimSpace(r.PostFormValue("docker_container")),
		DockerHealthy:          r.PostFormValue("docker_healthy") != "",
		NotifyEnabled:          true,
	}
	if r.PostForm.Has("notify_enabled") {
		values.NotifyEnabled = r.PostFormValue("notify_enabled") == "1"
	}
	return values
}

func buildMonitorFromValues(monitorType model.MonitorType, values monitorFormValues, base model.Monitor) (model.Monitor, error) {
	if monitorType != model.MonitorTypeHTTP && monitorType != model.MonitorTypeTCP &&
		monitorType != model.MonitorTypeDocker && monitorType != model.MonitorTypePush {
		return model.Monitor{}, newFormError("error.invalidRequest")
	}

	interval, err := strconv.Atoi(values.Interval)
	if err != nil {
		return base, newFormError("error.interval")
	}
	timeout, err := strconv.Atoi(values.Timeout)
	if err != nil {
		return base, newFormError("error.timeout")
	}
	failThreshold, err := strconv.Atoi(values.FailThreshold)
	if err != nil {
		return base, newFormError("error.threshold")
	}
	recoverThreshold, err := strconv.Atoi(values.RecoverThreshold)
	if err != nil {
		return base, newFormError("error.threshold")
	}
	reminders, err := model.ParseNotificationPolicy(
		values.WarningReminderCount,
		values.WarningReminderMinutes,
		values.FailureReminderCount,
		values.FailureReminderMinutes,
	)
	if err != nil {
		return base, newFormError(formErrorKey(err))
	}

	base.Name = values.Name
	base.Type = monitorType
	base.Interval = time.Duration(interval) * time.Second
	base.Timeout = time.Duration(timeout) * time.Second
	base.FailThreshold = failThreshold
	base.RecoverThreshold = recoverThreshold
	base.Reminders = reminders
	base.NotifyEnabled = values.NotifyEnabled
	base.ProxyID = nil
	base.NotificationChannelIDs = nil
	for _, rawID := range values.ChannelIDs {
		channelID, err := strconv.ParseInt(strings.TrimSpace(rawID), 10, 64)
		if err != nil || channelID <= 0 {
			return base, newFormError("error.notificationChannelNotFound")
		}
		base.NotificationChannelIDs = append(base.NotificationChannelIDs, channelID)
	}
	if monitorType == model.MonitorTypeHTTP || monitorType == model.MonitorTypeTCP {
		if values.ProxyID != "" {
			proxyID, err := strconv.ParseInt(values.ProxyID, 10, 64)
			if err != nil || proxyID <= 0 {
				return base, newFormError("error.proxyNotFound")
			}
			base.ProxyID = &proxyID
		}
	}

	switch monitorType {
	case model.MonitorTypeHTTP:
		method := strings.ToUpper(strings.TrimSpace(values.Method))
		if method == "" {
			method = http.MethodGet
		}
		expectedStatus := strings.TrimSpace(values.ExpectedStatus)
		if expectedStatus == "" {
			expectedStatus = "200-299"
		}
		certWarnDays := 14
		if strings.TrimSpace(values.CertWarnDays) != "" {
			certWarnDays, err = strconv.Atoi(values.CertWarnDays)
			if err != nil {
				return base, newFormError("error.certCheck")
			}
		}
		domainWarnDays := 30
		if strings.TrimSpace(values.DomainWarnDays) != "" {
			domainWarnDays, err = strconv.Atoi(values.DomainWarnDays)
			if err != nil {
				return base, newFormError("error.domainCheck")
			}
		}
		base.HTTP = &model.HTTPConfig{
			URL:             strings.TrimSpace(values.URL),
			Method:          method,
			ExpectedStatus:  expectedStatus,
			Keyword:         values.Keyword,
			FollowRedirects: values.FollowRedirects,
			TLSSkipVerify:   values.TLSSkipVerify,
			CertCheck:       values.CertCheck,
			CertWarnDays:    certWarnDays,
			DomainCheck:     values.DomainCheck,
			DomainWarnDays:  domainWarnDays,
		}
		base.TCP = nil
		base.Docker = nil
		if values.DomainCheck {
			if _, err := domains.DomainForURL(base.HTTP.URL); err != nil {
				return base, newFormError("error.domainCheck")
			}
		}
	case model.MonitorTypeTCP:
		port, err := strconv.Atoi(values.Port)
		if err != nil {
			return base, newFormError("error.port")
		}
		base.TCP = &model.TCPConfig{Host: strings.TrimSpace(values.Host), Port: port}
		base.HTTP = nil
		base.Docker = nil
	case model.MonitorTypeDocker:
		base.Docker = &model.DockerConfig{
			Container:      strings.TrimSpace(values.DockerContainer),
			RequireHealthy: values.DockerHealthy,
		}
		base.HTTP = nil
		base.TCP = nil
	case model.MonitorTypePush:
		base.HTTP = nil
		base.TCP = nil
		base.Docker = nil
	}

	if err := base.Validate(); err != nil {
		return base, newFormError(formErrorKey(err))
	}
	return base, nil
}

func (s *Server) handleMonitorNew(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	typeParam := r.URL.Query().Get("type")
	if typeParam == "" {
		data := s.authenticatedPage(r, auth, "monitor.chooseType")
		data.BackURL = "/"
		page := monitorTypesPage{pageData: data}
		s.render(w, "monitor_types", http.StatusOK, page)
		return
	}
	monitorType := model.MonitorType(typeParam)
	if monitorType != model.MonitorTypeHTTP && monitorType != model.MonitorTypeTCP &&
		monitorType != model.MonitorTypeDocker && monitorType != model.MonitorTypePush {
		s.renderError(w, r, http.StatusNotFound, "error.notFound")
		return
	}
	page := s.buildMonitorFormPage(r, auth, 0, monitorType, defaultFormValues(), "")
	proxies, err := s.store.ListProxies(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.Proxies = proxies
	if err := s.loadMonitorFormChannels(r, &page, true); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	s.render(w, "monitor_form", http.StatusOK, page)
}

func (s *Server) handleMonitorEditForm(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	}
	monitor, err := s.store.MonitorByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	}
	if err != nil {
		s.log.Error("load monitor for edit", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page := s.buildMonitorFormPage(r, auth, monitor.ID, monitor.Type, formValuesFromMonitor(monitor), "")
	proxies, err := s.store.ListProxies(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.Proxies = proxies
	if err := s.loadMonitorFormChannels(r, &page, false); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	s.render(w, "monitor_form", http.StatusOK, page)
}

func (s *Server) handleMonitorCreate(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	monitorType := model.MonitorType(r.PostFormValue("type"))
	if monitorType != model.MonitorTypeHTTP && monitorType != model.MonitorTypeTCP &&
		monitorType != model.MonitorTypeDocker && monitorType != model.MonitorTypePush {
		s.renderError(w, r, http.StatusBadRequest, "error.invalidRequest")
		return
	}
	values := parseFormValues(r)
	base := model.Monitor{Enabled: true, NotifyEnabled: true}
	monitor, err := buildMonitorFromValues(monitorType, values, base)
	if err != nil {
		s.renderMonitorFormError(w, r, auth, 0, monitorType, values, formErrorKey(err))
		return
	}
	if err := s.validateProxySelection(r, monitor); err != nil {
		s.renderMonitorFormError(w, r, auth, 0, monitorType, values, formErrorKey(err))
		return
	}
	if err := s.validateNotificationChannelSelection(r, monitor); err != nil {
		s.renderMonitorFormError(w, r, auth, 0, monitorType, values, formErrorKey(err))
		return
	}
	id, err := s.store.CreateMonitor(r.Context(), monitor, s.now().UTC())
	if err != nil {
		s.log.Error("create monitor", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if monitor.Type == model.MonitorTypePush {
		if err := s.createPushToken(r, id); err != nil {
			s.log.Error("create push token", "monitor_id", id, "error", err)
			_ = s.store.DeleteMonitor(r.Context(), id)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
	}
	if err := s.scheduler.Reload(id); err != nil {
		s.log.Warn("reload scheduler after create", "monitor_id", id, "error", err)
	}
	http.Redirect(w, r, fmt.Sprintf("/monitors/%d?notice=monitor_created", id), http.StatusSeeOther)
}

func (s *Server) createPushToken(r *http.Request, monitorID int64) error {
	token, err := secret.RandomToken(32)
	if err != nil {
		return err
	}
	encrypted, err := s.cipher.Encrypt(token)
	if err != nil {
		return err
	}
	return s.store.CreatePushToken(r.Context(), monitorID, secret.HashToken(token), encrypted, s.now().UTC())
}

func (s *Server) validateProxySelection(r *http.Request, monitor model.Monitor) error {
	if monitor.ProxyID == nil {
		return nil
	}
	if _, err := s.store.ProxyByID(r.Context(), *monitor.ProxyID); errors.Is(err, store.ErrNotFound) {
		return newFormError("error.proxyNotFound")
	} else if err != nil {
		return err
	}
	return nil
}

func (s *Server) validateNotificationChannelSelection(r *http.Request, monitor model.Monitor) error {
	for _, channelID := range monitor.NotificationChannelIDs {
		if _, err := s.store.NotificationChannelByID(r.Context(), channelID); errors.Is(err, store.ErrNotFound) {
			return newFormError("error.notificationChannelNotFound")
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) renderMonitorFormError(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, monitorID int64, monitorType model.MonitorType, values monitorFormValues, errorKey string) {
	page := s.buildMonitorFormPage(r, auth, monitorID, monitorType, values, errorKey)
	proxies, err := s.store.ListProxies(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.Proxies = proxies
	if err := s.loadMonitorFormChannels(r, &page, false); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	s.render(w, "monitor_form", http.StatusBadRequest, page)
}

func (s *Server) loadMonitorFormChannels(r *http.Request, page *monitorFormPage, defaultAll bool) error {
	channels, err := s.store.ListNotificationChannels(r.Context())
	if err != nil {
		return err
	}
	selected := make(map[int64]struct{}, len(page.Values.ChannelIDs))
	for _, rawID := range page.Values.ChannelIDs {
		if id, err := strconv.ParseInt(rawID, 10, 64); err == nil {
			selected[id] = struct{}{}
		}
	}
	applyDefaults := defaultAll && len(selected) == 0
	options := make([]notificationChannelOption, 0, len(channels))
	selectedIDs := make([]string, 0, len(channels))
	for _, channel := range channels {
		_, isSelected := selected[channel.ID]
		if applyDefaults && channel.Enabled {
			isSelected = true
		}
		if isSelected {
			selectedIDs = append(selectedIDs, strconv.FormatInt(channel.ID, 10))
		}
		options = append(options, notificationChannelOption{
			ID:        channel.ID,
			Name:      channel.Name,
			TypeLabel: notificationChannelTypeLabel(page.I18n, channel.Type),
			Enabled:   channel.Enabled,
			Selected:  isSelected,
		})
	}
	page.Channels = options
	if applyDefaults {
		page.Values.ChannelIDs = selectedIDs
	}
	return nil
}

func (s *Server) handleMonitorUpdate(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	}
	if !s.authorizedPost(w, r, auth) {
		return
	}
	existing, err := s.store.MonitorByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	}
	if err != nil {
		s.log.Error("load monitor for update", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	values := parseFormValues(r)
	monitor, err := buildMonitorFromValues(existing.Type, values, existing)
	if err != nil {
		s.renderMonitorFormError(w, r, auth, id, existing.Type, values, formErrorKey(err))
		return
	}
	if err := s.validateProxySelection(r, monitor); err != nil {
		s.renderMonitorFormError(w, r, auth, id, existing.Type, values, formErrorKey(err))
		return
	}
	if err := s.validateNotificationChannelSelection(r, monitor); err != nil {
		s.renderMonitorFormError(w, r, auth, id, existing.Type, values, formErrorKey(err))
		return
	}
	if err := s.store.UpdateMonitor(r.Context(), monitor, s.now().UTC()); err != nil {
		s.log.Error("update monitor", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if err := s.scheduler.Reload(id); err != nil {
		s.log.Warn("reload scheduler after update", "monitor_id", id, "error", err)
	}
	http.Redirect(w, r, fmt.Sprintf("/monitors/%d?notice=monitor_updated", id), http.StatusSeeOther)
}

func (s *Server) handleMonitorCheck(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	}
	if !s.authorizedPost(w, r, auth) {
		return
	}
	monitor, err := s.store.MonitorByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	} else if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if !monitor.Enabled {
		http.Redirect(w, r, fmt.Sprintf("/monitors/%d?notice=check_paused", id), http.StatusSeeOther)
		return
	}

	notice := "check_started"
	if err := s.scheduler.CheckNow(id); err != nil {
		notice = "check_running"
	}
	http.Redirect(w, r, fmt.Sprintf("/monitors/%d?notice=%s", id, notice), http.StatusSeeOther)
}

func (s *Server) handleMonitorToggle(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	}
	if !s.authorizedPost(w, r, auth) {
		return
	}
	monitor, err := s.store.MonitorByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	}
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}

	enabled := !monitor.Enabled
	if err := s.store.SetMonitorEnabled(r.Context(), id, enabled); err != nil {
		s.log.Error("toggle monitor", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if !enabled {
		if err := s.store.DeleteReminderStates(r.Context(), id); err != nil {
			s.log.Warn("clear reminder states after pause", "monitor_id", id, "error", err)
		}
		if err := s.store.CloseOpenDownEvents(r.Context(), id, s.now().UTC()); err != nil {
			s.log.Warn("close open down events after pause", "monitor_id", id, "error", err)
		}
	} else {
		if err := s.store.CloseOpenDownEvents(r.Context(), id, s.now().UTC()); err != nil {
			s.log.Warn("close stale down events after resume", "monitor_id", id, "error", err)
		}
		if err := s.store.ResetDownStatusSince(r.Context(), id, s.now().UTC()); err != nil {
			s.log.Warn("reset down status after resume", "monitor_id", id, "error", err)
		}
	}
	if err := s.scheduler.Reload(id); err != nil {
		s.log.Warn("reload scheduler after toggle", "monitor_id", id, "error", err)
	}
	notice := "monitor_paused"
	if enabled {
		notice = "monitor_resumed"
	}
	http.Redirect(w, r, fmt.Sprintf("/monitors/%d?notice=%s", id, notice), http.StatusSeeOther)
}

func (s *Server) handleMonitorDelete(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	}
	if !s.authorizedPost(w, r, auth) {
		return
	}
	if err := s.store.DeleteMonitor(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	} else if err != nil {
		s.log.Error("delete monitor", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCleanup()
	if err := s.store.PrunePublicStatusMonitorIDs(cleanupCtx, s.now().UTC()); err != nil {
		s.log.Warn("prune public status monitor ids after delete", "monitor_id", id, "error", err)
	}
	s.scheduler.Remove(id)
	http.Redirect(w, r, "/?notice=monitor_deleted", http.StatusSeeOther)
}

func (s *Server) handleMonitorDetail(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	}
	monitor, err := s.store.MonitorByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "error.monitorNotFound")
		return
	}
	if err != nil {
		s.log.Error("load monitor detail", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}

	now := s.now().UTC()
	stats24, err := s.store.UptimeStats(r.Context(), id, now.Add(-24*time.Hour))
	if err != nil {
		s.log.Error("load 24h stats", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	stats7d, err := s.store.UptimeStats(r.Context(), id, now.Add(-7*24*time.Hour))
	if err != nil {
		s.log.Error("load 7d stats", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	stats30Older, err := s.store.RollupStats(r.Context(), id, now.Add(-30*24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil {
		s.log.Error("load 30d rollup stats", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	stats90Older, err := s.store.RollupStats(r.Context(), id, now.Add(-90*24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil {
		s.log.Error("load 90d rollup stats", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	stats30 := combineUptimeStats(stats7d, stats30Older)
	stats90 := combineUptimeStats(stats7d, stats90Older)
	buckets, err := s.store.HourlyBucketsForMonitor(r.Context(), id, now.Add(-24*time.Hour))
	if err != nil {
		s.log.Error("load detail buckets", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	location := s.timezone(r.Context())
	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	daily30, err := s.store.DailyBucketsForTimezone(r.Context(), id, today.AddDate(0, 0, -30), today, location)
	if err != nil {
		s.log.Error("load 30d daily buckets", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	daily90, err := s.store.DailyBucketsForTimezone(r.Context(), id, today.AddDate(0, 0, -90), today, location)
	if err != nil {
		s.log.Error("load 90d daily buckets", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	checks, err := s.store.RecentChecks(r.Context(), id, 50)
	if err != nil {
		s.log.Error("load recent checks", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	events, err := s.store.RecentEvents(r.Context(), id, 10)
	if err != nil {
		s.log.Error("load monitor events", "monitor_id", id, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}

	page := s.buildMonitorDetailPage(r, auth, monitor, checks, events, stats24, stats7d, stats30, stats90, buckets, daily30, daily90, now)
	s.populateMonitorExtras(r, &page.Monitor, page.I18n, page.Location, monitor, now, auth.Admin.IsAdmin())
	s.render(w, "monitor_detail", http.StatusOK, page)
}

func (s *Server) populateMonitorExtras(r *http.Request, view *monitorDetailView, translator *i18n.Translator, location *time.Location, monitor model.Monitor, now time.Time, isAdmin bool) {
	view.NotificationLabel = s.monitorNotificationLabel(r, translator, monitor)
	if monitor.ProxyID != nil {
		if proxy, err := s.store.ProxyByID(r.Context(), *monitor.ProxyID); err == nil {
			view.ProxyName = proxy.Name
		}
	}
	if monitor.HTTP != nil {
		if monitor.HTTP.CertCheck {
			view.CertEnabled = true
			if cert, err := s.store.LatestCertificate(r.Context(), monitor.ID); err == nil {
				days := int(cert.NotAfter.Sub(now).Hours() / 24)
				view.Cert = &certificateDetailView{
					Issuer:      cert.Issuer,
					Subject:     cert.Subject,
					Expires:     formatTime(translator, cert.NotAfter, location),
					Days:        fmt.Sprintf("%d", days),
					Fingerprint: cert.Fingerprint,
					Warning:     days <= monitor.HTTP.CertWarnDays,
				}
			}
		}
		if monitor.HTTP.DomainCheck {
			view.DomainEnabled = true
			if domain, err := domains.DomainForURL(monitor.HTTP.URL); err == nil {
				if info, err := s.store.DomainInfo(r.Context(), domain); err == nil {
					days := 0
					daysText := "—"
					warning := domains.HasSuspensionStatus(info.Statuses)
					if !info.ExpiresAt.IsZero() {
						days = int(info.ExpiresAt.Sub(now).Hours() / 24)
						daysText = fmt.Sprintf("%d", days)
						warning = warning || days <= monitor.HTTP.DomainWarnDays
					}
					view.Domain = &domainDetailView{
						Domain:    info.Domain,
						Registrar: info.Registrar,
						Expires:   formatTime(translator, info.ExpiresAt, location),
						Days:      daysText,
						Statuses:  strings.Join(info.Statuses, ", "),
						Fetched:   formatTime(translator, info.FetchedAt, location),
						Error:     info.Error,
						Warning:   warning,
					}
				}
			}
		}
	}
	if monitor.Type == model.MonitorTypePush && isAdmin {
		if token, err := s.store.PushTokenByMonitorID(r.Context(), monitor.ID); err == nil {
			plaintext, err := s.cipher.Decrypt(token.TokenEnc)
			if err == nil {
				view.PushToken = plaintext
				view.PushURL = s.requestScheme(r) + "://" + s.requestHost(r) + "/api/push/" + plaintext
				view.PushLastHeartbeat = formatTime(translator, token.LastHeartbeat, location)
			} else {
				s.log.Warn("decrypt push token", "monitor_id", monitor.ID, "error", err)
			}
		}
	}
}

func (s *Server) monitorNotificationLabel(r *http.Request, translator *i18n.Translator, monitor model.Monitor) string {
	if !monitor.NotifyEnabled {
		return translator.T("monitor.notificationsOff")
	}
	if len(monitor.NotificationChannelIDs) == 0 {
		return translator.T("monitor.notificationsNone")
	}
	names := make([]string, 0, len(monitor.NotificationChannelIDs))
	for _, channelID := range monitor.NotificationChannelIDs {
		channel, err := s.store.NotificationChannelByID(r.Context(), channelID)
		if err != nil {
			continue
		}
		names = append(names, channel.Name)
	}
	if len(names) == 0 {
		return translator.T("monitor.notificationsNone")
	}
	separator := ", "
	if translator.Lang() == i18n.LangZH {
		separator = "、"
	}
	return strings.Join(names, separator)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	events, err := s.store.RecentEvents(r.Context(), 0, 200)
	if err != nil {
		s.log.Error("load events", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	data := s.authenticatedPage(r, auth, "events.title")
	rows := buildEventRows(data.I18n, events, true, data.Location)
	page := eventsPage{
		pageData:  data,
		Events:    rows,
		HasEvents: len(rows) > 0,
		Total:     len(rows),
	}
	for _, row := range rows {
		switch {
		case row.Ongoing:
			page.OngoingCount++
		case row.TypeClass == "status-down":
			page.DownCount++
		default:
			page.UpCount++
		}
	}
	s.render(w, "events", http.StatusOK, page)
}

func (s *Server) buildMonitorFormPage(r *http.Request, auth model.AuthInfo, monitorID int64, monitorType model.MonitorType, values monitorFormValues, errorKey string) monitorFormPage {
	titleKey := "monitor.newTitle"
	if monitorID > 0 {
		titleKey = "monitor.editTitle"
	} else {
		switch monitorType {
		case model.MonitorTypeHTTP:
			titleKey = "monitor.newHTTP"
		case model.MonitorTypeTCP:
			titleKey = "monitor.newTCP"
		case model.MonitorTypeDocker:
			titleKey = "monitor.newDocker"
		case model.MonitorTypePush:
			titleKey = "monitor.newPush"
		}
	}
	data := s.authenticatedPage(r, auth, titleKey)
	if errorKey != "" {
		data.Error = data.I18n.T(errorKey)
	}

	action := "/monitors"
	cancelURL := "/monitors/new"
	if monitorID > 0 {
		action = fmt.Sprintf("/monitors/%d", monitorID)
		cancelURL = fmt.Sprintf("/monitors/%d", monitorID)
	}
	data.BackURL = cancelURL
	return monitorFormPage{
		pageData:   data,
		IsEdit:     monitorID > 0,
		Type:       monitorType,
		TypeLabel:  s.typeLabel(data.I18n, monitorType),
		Action:     action,
		CancelURL:  cancelURL,
		Values:     values,
		ShowHTTP:   monitorType == model.MonitorTypeHTTP,
		ShowTCP:    monitorType == model.MonitorTypeTCP,
		ShowDocker: monitorType == model.MonitorTypeDocker,
		ShowPush:   monitorType == model.MonitorTypePush,
		ShowProxy:  monitorType == model.MonitorTypeHTTP || monitorType == model.MonitorTypeTCP,
	}
}

func (s *Server) buildMonitorDetailPage(r *http.Request, auth model.AuthInfo, monitor model.Monitor, checks []model.Check, events []model.Event, stats24, stats7d, stats30, stats90 model.UptimeStats, buckets, daily30, daily90 []model.HourBucket, now time.Time) monitorDetailPage {
	data := s.authenticatedPage(r, auth, "monitor.title")
	data.Title = monitor.Name
	data.BackURL = "/"
	translator := data.I18n

	view := monitorDetailView{
		ID:                monitor.ID,
		Name:              monitor.Name,
		TypeLabel:         s.typeLabel(translator, monitor.Type),
		Target:            s.monitorTarget(translator, monitor),
		IsPush:            monitor.Type == model.MonitorTypePush,
		Enabled:           monitor.Enabled,
		StatusClass:       statusClass(monitor.Enabled, monitor.State.Status),
		StatusLabel:       statusLabel(translator, monitor.Enabled, monitor.State.Status),
		StatusSince:       formatTime(translator, monitor.State.StatusSince, data.Location),
		StatusSinceAgo:    formatRelativeTime(translator, now, monitor.State.StatusSince),
		LastCheckAgo:      formatRelativeTime(translator, now, monitor.State.LastCheck),
		LastCheckTitle:    formatTime(translator, monitor.State.LastCheck, data.Location),
		Interval:          secondsLabel(monitor.Interval),
		Timeout:           secondsLabel(monitor.Timeout),
		FailThreshold:     monitor.FailThreshold,
		RecoverThreshold:  monitor.RecoverThreshold,
		Uptime24:          formatPercent(stats24.Up+stats24.Warning, stats24.Total),
		Uptime7d:          formatPercent(stats7d.Up+stats7d.Warning, stats7d.Total),
		Uptime30d:         formatPercent(stats30.Up+stats30.Warning, stats30.Total),
		Uptime90d:         formatPercent(stats90.Up+stats90.Warning, stats90.Total),
		AvgLatency24:      formatLatency(stats24.AvgLatency),
		LastLatency:       "—",
		RecentBarsSummary: summarizeChecks(translator, checks, 24),
		BarsSummary:       summarizeHourBuckets(translator, now, buckets),
		ChartRecent:       buildRecentChart(translator, checks, 24, data.Location),
		Chart24:           buildHourlyChart(translator, now, buckets, data.Location),
		Chart30:           buildDailyChart(translator, now, daily30, 30, data.Location, "monitor.uptime30dChart"),
		Chart90:           buildDailyChart(translator, now, daily90, 90, data.Location, "monitor.uptime90dChart"),
		Events:            buildEventRows(translator, events, false, data.Location),
	}
	if monitor.HTTP != nil {
		view.Method = monitor.HTTP.Method
	}
	if len(checks) > 0 {
		view.LastLatency = formatLatency(checks[0].Latency)
	}
	return monitorDetailPage{pageData: data, Monitor: view}
}
