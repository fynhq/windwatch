package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/fynhq/windwatch/internal/checker"
	"github.com/fynhq/windwatch/internal/config"
	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/notify"
	"github.com/fynhq/windwatch/internal/scheduler"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
	"github.com/fynhq/windwatch/internal/version"
	"github.com/fynhq/windwatch/internal/web"
)

type App struct {
	cfg       config.Config
	log       *slog.Logger
	store     *store.Store
	scheduler *scheduler.Scheduler
	notifier  *notify.Service
	server    *http.Server
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}

	dbPath := filepath.Join(cfg.DataDir, "windwatch.db")
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			st.Close()
		}
	}()

	if err := st.Migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	startupNow := time.Now().UTC()
	if err := st.PrunePublicStatusMonitorIDs(context.Background(), startupNow); err != nil {
		logger.Warn("prune public status monitor ids", "error", err)
	}

	catalogs, err := i18n.Load()
	if err != nil {
		return nil, fmt.Errorf("load translations: %w", err)
	}
	key, err := secret.LoadOrCreateKey(filepath.Join(cfg.DataDir, "secret.key"))
	if err != nil {
		return nil, fmt.Errorf("load secret key: %w", err)
	}
	cipher, err := secret.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create secret cipher: %w", err)
	}
	monitorScheduler := scheduler.New(st, checker.NewRegistry(), logger)
	monitorScheduler.SetCipher(cipher)
	notificationService := notify.New(st, cipher, catalogs, logger)
	monitorScheduler.SetNotifier(notificationService)
	webServer, err := web.New(st, catalogs, monitorScheduler, notificationService, cipher, cfg.TrustedProxies, logger, version.Version)
	if err != nil {
		return nil, fmt.Errorf("create web server: %w", err)
	}

	application := &App{
		cfg:       cfg,
		log:       logger,
		store:     st,
		scheduler: monitorScheduler,
		notifier:  notificationService,
		server: &http.Server{
			Addr:              cfg.Addr,
			Handler:           webServer.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}
	closeOnError = false
	return application, nil
}

func (a *App) Run(ctx context.Context) error {
	defer a.store.Close()

	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 5*time.Second)
	if err := a.store.DeleteExpiredSessions(cleanupCtx, time.Now().UTC()); err != nil {
		a.log.Warn("clean expired sessions", "error", err)
	}
	cancelCleanup()

	schedulerCtx, cancelScheduler := context.WithCancel(ctx)
	a.notifier.Start(schedulerCtx)
	if err := a.scheduler.Start(schedulerCtx); err != nil {
		cancelScheduler()
		a.notifier.Wait()
		return fmt.Errorf("start scheduler: %w", err)
	}
	defer func() {
		cancelScheduler()
		a.scheduler.Wait()
		a.notifier.Wait()
	}()

	listener, err := net.Listen("tcp", a.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", a.cfg.Addr, err)
	}

	a.log.Info("windwatch starting",
		"version", version.Version,
		"addr", listener.Addr().String(),
		"data_dir", a.cfg.DataDir,
	)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- a.server.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
		a.log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		err := <-serveErr
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve http: %w", err)
		}
		return nil
	}
}
