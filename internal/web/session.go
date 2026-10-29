package web

import (
	"crypto/subtle"
	"net/http"
	"sync"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
)

const (
	sessionCookieName  = "windwatch_session"
	languageCookieName = "windwatch_lang"
	csrfCookieName     = "windwatch_csrf"

	sessionDuration = 30 * 24 * time.Hour
	csrfCookieAge   = time.Hour
)

type loginAttempt struct {
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
}

type loginLimiter struct {
	mu        sync.Mutex
	entries   map[string]*loginAttempt
	lastPrune time.Time
}

const (
	loginFailureWindow = 5 * time.Minute
	loginBlockDuration = 5 * time.Minute
	maxLoginFailures   = 5
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: make(map[string]*loginAttempt)}
}

func (l *loginLimiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)

	entry := l.entries[key]
	if entry == nil {
		return false
	}
	if !entry.blockedUntil.IsZero() && now.Before(entry.blockedUntil) {
		return true
	}
	if !entry.blockedUntil.IsZero() || now.Sub(entry.windowStart) >= loginFailureWindow {
		delete(l.entries, key)
	}
	return false
}

func (l *loginLimiter) failure(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.entries[key]
	if entry == nil || now.Sub(entry.windowStart) >= loginFailureWindow || (!entry.blockedUntil.IsZero() && !now.Before(entry.blockedUntil)) {
		entry = &loginAttempt{windowStart: now}
		l.entries[key] = entry
	}
	entry.failures++
	if entry.failures >= maxLoginFailures {
		entry.blockedUntil = now.Add(loginBlockDuration)
	}
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

func (l *loginLimiter) pruneLocked(now time.Time) {
	if !l.lastPrune.IsZero() && now.Sub(l.lastPrune) < loginFailureWindow {
		return
	}
	l.lastPrune = now
	for key, entry := range l.entries {
		if !entry.blockedUntil.IsZero() {
			if !now.Before(entry.blockedUntil) {
				delete(l.entries, key)
			}
			continue
		}
		if now.Sub(entry.windowStart) >= loginFailureWindow {
			delete(l.entries, key)
		}
	}
}

func (s *Server) authFromRequest(r *http.Request) (model.AuthInfo, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return model.AuthInfo{}, false
	}
	auth, err := s.store.SessionByTokenHash(r.Context(), secret.HashToken(cookie.Value), s.now())
	if err != nil {
		return model.AuthInfo{}, false
	}
	return auth, true
}

func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request, model.AuthInfo)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hasAdmin, err := s.store.HasAdmin(r.Context())
		if err != nil {
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		if !hasAdmin {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		auth, ok := s.authFromRequest(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, auth)
	}
}

func (s *Server) requireAdmin(next func(http.ResponseWriter, *http.Request, model.AuthInfo)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
		if !auth.Admin.IsAdmin() {
			s.renderError(w, r, http.StatusForbidden, "error.forbidden")
			return
		}
		next(w, r, auth)
	})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request, adminID int64) error {
	token, err := secret.RandomToken(32)
	if err != nil {
		return err
	}
	csrfToken, err := secret.RandomToken(32)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	session := model.Session{
		TokenHash: secret.HashToken(token),
		AdminID:   adminID,
		CSRFToken: csrfToken,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionDuration),
	}
	if err := s.store.CreateSession(r.Context(), session); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  session.ExpiresAt,
		MaxAge:   int(sessionDuration.Seconds()),
	})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) ensurePreAuthCSRF(w http.ResponseWriter, r *http.Request) (string, error) {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		return cookie.Value, nil
	}
	token, err := secret.RandomToken(32)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(csrfCookieAge.Seconds()),
	})
	return token, nil
}

func (s *Server) validatePreAuthCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	formToken := r.PostFormValue("csrf_token")
	if formToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(formToken), []byte(cookie.Value)) == 1
}

func (s *Server) authorizedPost(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) bool {
	if !s.parseForm(w, r) {
		return false
	}
	if !s.validateSessionCSRF(r, auth) {
		s.renderError(w, r, http.StatusForbidden, "error.csrf")
		return false
	}
	return true
}

func (s *Server) validateSessionCSRF(r *http.Request, auth model.AuthInfo) bool {
	formToken := r.PostFormValue("csrf_token")
	if formToken == "" || auth.Session.CSRFToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(formToken), []byte(auth.Session.CSRFToken)) == 1
}

func (s *Server) secureCookie(r *http.Request) bool {
	return s.requestScheme(r) == "https"
}

func (s *Server) clientKey(r *http.Request) string {
	return clientIP(r, s.trustedProxies)
}
