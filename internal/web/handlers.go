package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

func (s *Server) handleSetupGet(w http.ResponseWriter, r *http.Request) {
	hasAdmin, err := s.store.HasAdmin(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if hasAdmin {
		s.renderError(w, r, http.StatusNotFound, "error.setupClosed")
		return
	}
	csrf, err := s.ensurePreAuthCSRF(w, r)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	s.renderSetupPage(w, r, http.StatusOK, csrf, "", "")
}

func (s *Server) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	hasAdmin, err := s.store.HasAdmin(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if hasAdmin {
		s.renderError(w, r, http.StatusNotFound, "error.setupClosed")
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	csrf, err := s.ensurePreAuthCSRF(w, r)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if !s.validatePreAuthCSRF(r) {
		s.renderSetupPage(w, r, http.StatusForbidden, csrf, strings.TrimSpace(r.PostFormValue("username")), "error.csrf")
		return
	}

	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("password_confirm")

	switch {
	case utf8.RuneCountInString(username) < 3:
		s.renderSetupPage(w, r, http.StatusBadRequest, csrf, username, "error.usernameShort")
		return
	case utf8.RuneCountInString(username) > 64:
		s.renderSetupPage(w, r, http.StatusBadRequest, csrf, username, "error.usernameLong")
		return
	case utf8.RuneCountInString(password) < 10:
		s.renderSetupPage(w, r, http.StatusBadRequest, csrf, username, "error.passwordShort")
		return
	case utf8.RuneCountInString(password) > 256:
		s.renderSetupPage(w, r, http.StatusBadRequest, csrf, username, "error.passwordLong")
		return
	case password != confirm:
		s.renderSetupPage(w, r, http.StatusBadRequest, csrf, username, "error.passwordMismatch")
		return
	}

	passwordHash, err := secret.HashPassword(password)
	if err != nil {
		s.log.Error("hash administrator password", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	admin, err := s.store.CreateFirstAdmin(r.Context(), username, passwordHash, s.now())
	if errors.Is(err, store.ErrAdminExists) {
		s.renderError(w, r, http.StatusNotFound, "error.setupClosed")
		return
	}
	if err != nil {
		s.log.Error("create administrator", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	s.log.Info("administrator created", "username", admin.Username)

	if err := s.createSession(w, r, admin.ID); err != nil {
		s.log.Error("create session after setup", "error", err)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	hasAdmin, err := s.store.HasAdmin(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if !hasAdmin {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if _, ok := s.authFromRequest(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	csrf, err := s.ensurePreAuthCSRF(w, r)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	s.renderLoginPage(w, r, http.StatusOK, csrf, "", "")
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	hasAdmin, err := s.store.HasAdmin(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if !hasAdmin {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	csrf, err := s.ensurePreAuthCSRF(w, r)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	if !s.validatePreAuthCSRF(r) {
		s.renderLoginPage(w, r, http.StatusForbidden, csrf, username, "error.csrf")
		return
	}

	key := s.clientKey(r)
	now := s.now()
	if s.limiter.blocked(key, now) {
		s.renderLoginPage(w, r, http.StatusTooManyRequests, csrf, username, "error.tooManyAttempts")
		return
	}

	admin, err := s.store.AdminByUsername(r.Context(), username)
	if errors.Is(err, store.ErrNotFound) {
		s.limiter.failure(key, now)
		s.log.Warn("login failed", "username", username, "reason", "unknown user", "client", key)
		s.renderLoginPage(w, r, http.StatusUnauthorized, csrf, username, "error.invalidCredentials")
		return
	}
	if err != nil {
		s.log.Error("load administrator", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}

	verified, err := secret.VerifyPassword(admin.PasswordHash, r.PostFormValue("password"))
	if err != nil {
		s.log.Error("verify administrator password", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if !verified || admin.Disabled {
		s.limiter.failure(key, now)
		reason := "invalid credentials"
		if verified {
			reason = "disabled account"
		}
		s.log.Warn("login failed", "username", username, "reason", reason, "client", key)
		s.renderLoginPage(w, r, http.StatusUnauthorized, csrf, username, "error.invalidCredentials")
		return
	}

	s.limiter.reset(key)
	if err := s.createSession(w, r, admin.ID); err != nil {
		s.log.Error("create login session", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	s.log.Info("user logged in", "username", admin.Username, "client", key)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	if err := s.store.DeleteSession(r.Context(), auth.Session.TokenHash); err != nil {
		s.log.Error("delete session", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	s.clearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	now := s.now().UTC()
	monitors, err := s.store.ListMonitors(r.Context())
	if err != nil {
		s.log.Error("list monitors for dashboard", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	buckets, err := s.store.HourlyBuckets(r.Context(), now.Add(-24*time.Hour))
	if err != nil {
		s.log.Error("load dashboard buckets", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	monitorIDs := make([]int64, 0, len(monitors))
	for _, monitor := range monitors {
		monitorIDs = append(monitorIDs, monitor.ID)
	}
	recentChecks, err := s.store.RecentChecksByMonitor(r.Context(), monitorIDs, 24)
	if err != nil {
		s.log.Error("load dashboard recent checks", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}

	data := s.authenticatedPage(r, auth, "dashboard.title")
	page := dashboardPage{pageData: data, Total: len(monitors), HasMonitors: len(monitors) > 0}
	translator := data.I18n
	for _, monitor := range monitors {
		good, total := summarizeLast24Hours(now, buckets[monitor.ID])
		checks := recentChecks[monitor.ID]
		lastLatency := "—"
		if len(checks) > 0 {
			lastLatency = formatLatency(checks[0].Latency)
		}
		barsStart, barsEnd := checkBarRange(translator, now, checks, 24)
		card := monitorCard{
			ID:             monitor.ID,
			Name:           monitor.Name,
			TypeLabel:      s.typeLabel(translator, monitor.Type),
			Target:         s.monitorTarget(translator, monitor),
			StatusClass:    statusClass(monitor.Enabled, monitor.State.Status),
			StatusKey:      statusKey(monitor.Enabled, monitor.State.Status),
			StatusLabel:    statusLabel(translator, monitor.Enabled, monitor.State.Status),
			SearchText:     strings.ToLower(monitor.Name + " " + s.monitorTarget(translator, monitor) + " " + s.typeLabel(translator, monitor.Type)),
			LastCheckAgo:   formatRelativeTime(translator, now, monitor.State.LastCheck),
			LastCheckTitle: formatTime(translator, monitor.State.LastCheck, data.Location),
			LastLatency:    lastLatency,
			IntervalLabel:  translator.Tf("dashboard.checkEvery", formatInterval(monitor.Interval)),
			Uptime24:       formatPercent(good, total),
			Bars:           buildCheckBars(translator, checks, 24, data.Location),
			BarsSummary:    summarizeChecks(translator, checks, 24),
			BarsStart:      barsStart,
			BarsEnd:        barsEnd,
		}
		page.Monitors = append(page.Monitors, card)

		if !monitor.Enabled {
			page.Paused++
			continue
		}
		switch monitor.State.Status {
		case model.StatusUp:
			page.Up++
		case model.StatusWarning:
			page.Warning++
		case model.StatusDown:
			page.Down++
		default:
			page.Pending++
		}
	}
	page.OverallClass, page.OverallLabel = dashboardOverall(translator, page.Total, page.Up, page.Warning, page.Down, page.Pending, page.Paused)
	page.AutoRefresh = true
	if r.Header.Get(partialRefreshHeader) == "dashboard" {
		s.renderFragment(w, "dashboard", http.StatusOK, page)
		return
	}
	s.render(w, "dashboard", http.StatusOK, page)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status := "ok"
	code := http.StatusOK
	if err := s.store.Ping(ctx); err != nil {
		status = "unavailable"
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  status,
		"version": s.version,
	})
}

func (s *Server) handleLanguage(w http.ResponseWriter, r *http.Request) {
	lang := r.PathValue("lang")
	switch lang {
	case "zh-CN", "en":
	default:
		s.renderError(w, r, http.StatusNotFound, "error.notFound")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     languageCookieName,
		Value:    lang,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
	})
	next := r.URL.Query().Get("next")
	if !safeRedirectPath(next) {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func safeRedirectPath(value string) bool {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return false
	}
	if strings.ContainsAny(value, "\\\r\n") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
		return false
	}
	return true
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, r, http.StatusNotFound, "error.notFound")
}

func pathID(r *http.Request) (int64, bool) {
	value, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func (s *Server) parseForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "error.invalidRequest")
		return false
	}
	return true
}

func (s *Server) renderSetupPage(w http.ResponseWriter, r *http.Request, status int, csrf, username, errorKey string) {
	data := s.newPage(r, "setup.title")
	data.CSRF = csrf
	data.Username = username
	if errorKey != "" {
		data.Error = data.I18n.T(errorKey)
	}
	s.render(w, "setup", status, data)
}

func (s *Server) renderLoginPage(w http.ResponseWriter, r *http.Request, status int, csrf, username, errorKey string) {
	data := s.newPage(r, "login.title")
	data.CSRF = csrf
	data.Username = username
	if errorKey != "" {
		data.Error = data.I18n.T(errorKey)
	}
	s.render(w, "login", status, data)
}
