package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"path"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

type MonitorScheduler interface {
	Reload(monitorID int64) error
	Remove(monitorID int64)
	CheckNow(monitorID int64) error
}

type NotificationTester interface {
	Test(ctx context.Context, channelID int64) error
}

type Server struct {
	store          *store.Store
	catalogs       *i18n.Catalogs
	scheduler      MonitorScheduler
	notifier       NotificationTester
	cipher         *secret.Cipher
	trustedProxies []netip.Prefix
	log            *slog.Logger
	version        string
	assetVersion   string
	pages          map[string]*template.Template
	staticFS       fs.FS
	limiter        *loginLimiter
	pushLimiter    *pushLimiter
	now            func() time.Time
}

func New(st *store.Store, catalogs *i18n.Catalogs, scheduler MonitorScheduler, notifier NotificationTester, cipher *secret.Cipher, trustedProxies []netip.Prefix, logger *slog.Logger, version string) (*Server, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if catalogs == nil {
		return nil, fmt.Errorf("i18n catalogs are required")
	}
	if scheduler == nil {
		return nil, fmt.Errorf("scheduler is required")
	}
	if notifier == nil {
		return nil, fmt.Errorf("notifier is required")
	}
	if cipher == nil {
		return nil, fmt.Errorf("secret cipher is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	pages := make(map[string]*template.Template, 8)
	for _, page := range []string{
		"login", "setup", "dashboard", "error",
		"monitor_types", "monitor_form", "monitor_detail", "events", "settings", "settings_index", "users", "status",
	} {
		parsed, err := template.New("layout").ParseFS(assets,
			"templates/layout.html",
			"templates/"+page+".html",
		)
		if err != nil {
			return nil, fmt.Errorf("parse page %s: %w", page, err)
		}
		pages[page] = parsed
	}

	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, fmt.Errorf("open embedded static files: %w", err)
	}
	assetVersion, err := staticAssetVersion(staticFS)
	if err != nil {
		return nil, fmt.Errorf("hash static assets: %w", err)
	}

	return &Server{
		store:          st,
		catalogs:       catalogs,
		scheduler:      scheduler,
		notifier:       notifier,
		cipher:         cipher,
		trustedProxies: trustedProxies,
		log:            logger,
		version:        version,
		assetVersion:   assetVersion,
		pages:          pages,
		staticFS:       staticFS,
		limiter:        newLoginLimiter(),
		pushLimiter:    newPushLimiter(),
		now:            time.Now,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /static/", s.handleStatic)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /language/{lang}", s.handleLanguage)
	mux.HandleFunc("GET /status", s.handlePublicStatus)
	mux.HandleFunc("GET /setup", s.handleSetupGet)
	mux.HandleFunc("POST /setup", s.handleSetupPost)
	mux.HandleFunc("GET /login", s.handleLoginGet)
	mux.HandleFunc("POST /login", s.handleLoginPost)
	mux.HandleFunc("POST /logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET /{$}", s.requireAuth(s.handleDashboard))
	mux.HandleFunc("GET /events", s.requireAuth(s.handleEvents))
	mux.HandleFunc("GET /settings", s.requireAdmin(s.handleSettingsIndex))
	mux.HandleFunc("GET /settings/general", s.requireAdmin(s.handleSettings))
	mux.HandleFunc("GET /settings/public", s.requireAdmin(s.handleSettings))
	mux.HandleFunc("GET /settings/notifications", s.requireAdmin(s.handleSettings))
	mux.HandleFunc("GET /settings/logs", s.requireAdmin(s.handleSettings))
	mux.HandleFunc("GET /settings/proxies", s.requireAdmin(s.handleSettings))
	mux.HandleFunc("GET /settings/users", s.requireAdmin(s.handleUsers))
	mux.HandleFunc("POST /settings/users", s.requireAdmin(s.handleUserCreate))
	mux.HandleFunc("POST /settings/users/{id}/role", s.requireAdmin(s.handleUserRole))
	mux.HandleFunc("POST /settings/users/{id}/disabled", s.requireAdmin(s.handleUserDisabled))
	mux.HandleFunc("POST /settings/users/{id}/delete", s.requireAdmin(s.handleUserDelete))
	mux.HandleFunc("POST /settings/users/{id}/password", s.requireAdmin(s.handleUserPassword))
	mux.HandleFunc("POST /settings/proxies", s.requireAdmin(s.handleProxyCreate))
	mux.HandleFunc("POST /settings/proxies/test", s.requireAdmin(s.handleProxyTest))
	mux.HandleFunc("POST /settings/proxies/{id}", s.requireAdmin(s.handleProxyUpdate))
	mux.HandleFunc("POST /settings/proxies/{id}/test", s.requireAdmin(s.handleProxyTest))
	mux.HandleFunc("POST /settings/proxies/{id}/delete", s.requireAdmin(s.handleProxyDelete))
	mux.HandleFunc("POST /settings/public-status", s.requireAdmin(s.handlePublicStatusSave))
	mux.HandleFunc("POST /settings/timezone", s.requireAdmin(s.handleTimezoneSave))
	mux.HandleFunc("POST /settings/notifications/channels", s.requireAdmin(s.handleNotificationChannelCreate))
	mux.HandleFunc("POST /settings/notifications/channels/{id}", s.requireAdmin(s.handleNotificationChannelUpdate))
	mux.HandleFunc("POST /settings/notifications/channels/{id}/delete", s.requireAdmin(s.handleNotificationChannelDelete))
	mux.HandleFunc("POST /settings/notifications/channels/{id}/test", s.requireAdmin(s.handleNotificationTest))
	mux.HandleFunc("GET /monitors/new", s.requireAdmin(s.handleMonitorNew))
	mux.HandleFunc("POST /monitors", s.requireAdmin(s.handleMonitorCreate))
	mux.HandleFunc("GET /monitors/{id}", s.requireAuth(s.handleMonitorDetail))
	mux.HandleFunc("GET /monitors/{id}/edit", s.requireAdmin(s.handleMonitorEditForm))
	mux.HandleFunc("POST /monitors/{id}", s.requireAdmin(s.handleMonitorUpdate))
	mux.HandleFunc("POST /monitors/{id}/check", s.requireAdmin(s.handleMonitorCheck))
	mux.HandleFunc("POST /monitors/{id}/toggle", s.requireAdmin(s.handleMonitorToggle))
	mux.HandleFunc("POST /monitors/{id}/delete", s.requireAdmin(s.handleMonitorDelete))
	mux.HandleFunc("GET /api/push/{token}", s.handlePush)
	mux.HandleFunc("POST /api/push/{token}", s.handlePush)
	mux.HandleFunc("/", s.handleNotFound)

	return s.withSecurityHeaders(s.withPanicRecovery(s.withBodyLimit(mux)))
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if name == "" || !fs.ValidPath(name) || strings.HasSuffix(name, "/") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(s.staticFS, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}

// staticAssetVersion fingerprints the embedded assets so template URLs change
// whenever a file changes; browsers otherwise keep stale CSS after upgrades.
func staticAssetVersion(staticFS fs.FS) (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(staticFS, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(staticFS, name)
		if err != nil {
			return err
		}
		hash.Write([]byte(name))
		hash.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil))[:12], nil
}

func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "same-origin")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		header.Set("X-Robots-Tag", "noindex, nofollow")
		if s.requestScheme(r) == "https" {
			header.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withPanicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.Error("http handler panic", "path", requestLogPath(r), "error", recovered)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
