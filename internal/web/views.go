package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
)

type pageData struct {
	I18n            *i18n.Translator
	Title           string
	Path            string
	Version         string
	AssetVersion    string
	Username        string
	CSRF            string
	Error           string
	Notice          string
	PublicView      bool
	Authenticated   bool
	IsAdmin         bool
	AutoRefresh     bool
	ActiveNav       string
	SettingsSection string
	BackURL         string
	Location        *time.Location
}

var noticeKeys = map[string]string{
	"monitor_created":                  "notice.monitorCreated",
	"monitor_updated":                  "notice.monitorUpdated",
	"monitor_deleted":                  "notice.monitorDeleted",
	"monitor_paused":                   "notice.monitorPaused",
	"monitor_resumed":                  "notice.monitorResumed",
	"check_started":                    "notice.checkStarted",
	"check_running":                    "notice.checkRunning",
	"check_paused":                     "notice.checkPaused",
	"proxy_created":                    "notice.proxyCreated",
	"proxy_updated":                    "notice.proxyUpdated",
	"proxy_deleted":                    "notice.proxyDeleted",
	"proxy_test_ok":                    "notice.proxyTestOK",
	"notification_channel_created":     "notice.notificationChannelCreated",
	"notification_channel_updated":     "notice.notificationChannelUpdated",
	"notification_channel_deleted":     "notice.notificationChannelDeleted",
	"notification_channel_test_sent":   "notice.notificationChannelTestSent",
	"notification_channel_test_failed": "notice.notificationChannelTestFailed",
	"public_status_saved":              "notice.publicStatusSaved",
	"timezone_saved":                   "notice.timezoneSaved",
	"user_created":                     "notice.userCreated",
	"user_updated":                     "notice.userUpdated",
	"user_deleted":                     "notice.userDeleted",
	"user_disabled":                    "notice.userDisabled",
	"user_enabled":                     "notice.userEnabled",
	"user_password_reset":              "notice.userPasswordReset",
	"password_reset":                   "notice.passwordReset",
}

func (s *Server) translator(r *http.Request) *i18n.Translator {
	lang := i18n.Negotiate(s.languageCookie(r), r.Header.Get("Accept-Language"))
	return s.catalogs.Translator(lang)
}

func (s *Server) languageCookie(r *http.Request) string {
	cookie, err := r.Cookie(languageCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) newPage(r *http.Request, titleKey string) pageData {
	translator := s.translator(r)
	notice := ""
	if key, ok := noticeKeys[r.URL.Query().Get("notice")]; ok {
		notice = translator.T(key)
	}
	return pageData{
		I18n:         translator,
		Title:        translator.T(titleKey),
		Path:         r.URL.Path,
		Version:      s.version,
		AssetVersion: s.assetVersion,
		Notice:       notice,
		Location:     s.timezone(r.Context()),
	}
}

func (s *Server) timezone(ctx context.Context) *time.Location {
	values, err := s.store.Settings(ctx)
	if err != nil {
		return time.UTC
	}
	return model.TimezoneFromSettings(values)
}

func (s *Server) authenticatedPage(r *http.Request, auth model.AuthInfo, titleKey string) pageData {
	data := s.newPage(r, titleKey)
	data.Username = auth.Admin.Username
	data.CSRF = auth.Session.CSRFToken
	data.Authenticated = true
	data.IsAdmin = auth.Admin.IsAdmin()
	data.ActiveNav = "dashboard"
	switch {
	case r.URL.Path == "/events":
		data.ActiveNav = "events"
	case strings.HasPrefix(r.URL.Path, "/settings/users"):
		data.ActiveNav = "users"
	case strings.HasPrefix(r.URL.Path, "/settings"):
		data.ActiveNav = "settings"
		data.SettingsSection = settingsSectionForPath(r.URL.Path)
		if r.URL.Path == "/settings" {
			data.BackURL = "/"
		} else {
			data.BackURL = "/settings"
		}
	}
	return data
}

func settingsSectionForPath(path string) string {
	if path == "/settings" {
		return "overview"
	}
	switch {
	case strings.HasPrefix(path, "/settings/public"):
		return "public"
	case strings.HasPrefix(path, "/settings/notifications"):
		return "notifications"
	case strings.HasPrefix(path, "/settings/logs"):
		return "logs"
	case strings.HasPrefix(path, "/settings/proxies"):
		return "proxies"
	default:
		return "general"
	}
}

func formatTime(translator *i18n.Translator, value time.Time, location *time.Location) string {
	if value.IsZero() {
		return translator.T("common.never")
	}
	if location == nil {
		location = time.UTC
	}
	value = value.In(location)
	if translator.Lang() == i18n.LangZH {
		return value.Format("2006-01-02 15:04:05")
	}
	return value.Format("Jan 2, 2006 15:04:05")
}

func formatRelativeTime(translator *i18n.Translator, now, value time.Time) string {
	if value.IsZero() {
		return translator.T("common.never")
	}
	elapsed := now.Sub(value)
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < 10*time.Second:
		return translator.T("common.justNow")
	case elapsed < time.Minute:
		return translator.Tf("common.secondsAgo", int(elapsed/time.Second))
	case elapsed < time.Hour:
		return translator.Tf("common.minutesAgo", int(elapsed/time.Minute))
	case elapsed < 24*time.Hour:
		return translator.Tf("common.hoursAgo", int(elapsed/time.Hour))
	case elapsed < 48*time.Hour:
		return translator.T("common.dayAgo")
	default:
		return translator.Tf("common.daysAgo", int(elapsed/(24*time.Hour)))
	}
}

func formatInterval(interval time.Duration) string {
	if interval <= 0 {
		return "—"
	}
	switch {
	case interval%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", int(interval/(24*time.Hour)))
	case interval%time.Hour == 0:
		return fmt.Sprintf("%dh", int(interval/time.Hour))
	case interval%time.Minute == 0:
		return fmt.Sprintf("%dm", int(interval/time.Minute))
	default:
		return fmt.Sprintf("%ds", int(interval/time.Second))
	}
}

func formatLatency(latency time.Duration) string {
	if latency <= 0 {
		return "—"
	}
	if latency < time.Millisecond {
		return "<1 ms"
	}
	return fmt.Sprintf("%d ms", latency.Milliseconds())
}

func formatPercent(part, total int) string {
	if total <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f%%", float64(part)*100/float64(total))
}

const partialRefreshHeader = "X-WindWatch-Partial"

func (s *Server) render(w http.ResponseWriter, page string, status int, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := s.pages[page].ExecuteTemplate(w, "layout", data); err != nil {
		s.log.Error("render page", "page", page, "error", err)
	}
}

// renderFragment returns only the live region for a page that supports
// in-place polling. It shares the page template so server-rendered content
// cannot drift from the full-page output.
func (s *Server) renderFragment(w http.ResponseWriter, page string, status int, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", partialRefreshHeader)
	w.WriteHeader(status)
	if err := s.pages[page].ExecuteTemplate(w, page+"-live", data); err != nil {
		s.log.Error("render fragment", "page", page, "error", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, messageKey string) {
	if status >= http.StatusInternalServerError {
		s.log.Error("request failed", "status", status, "path", requestLogPath(r), "message_key", messageKey)
	}
	data := s.newPage(r, "error.title")
	if auth, ok := s.authFromRequest(r); ok {
		data.Username = auth.Admin.Username
		data.CSRF = auth.Session.CSRFToken
		data.Authenticated = true
		data.IsAdmin = auth.Admin.IsAdmin()
	}
	data.Error = data.I18n.T(messageKey)
	s.render(w, "error", status, data)
}

// requestLogPath keeps notification/Push secrets out of application logs.
func requestLogPath(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/api/push/") {
		return "/api/push/[redacted]"
	}
	return r.URL.Path
}
