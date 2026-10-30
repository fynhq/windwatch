package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/fynhq/windwatch/internal/checker"
	"github.com/fynhq/windwatch/internal/domains"
	"github.com/fynhq/windwatch/internal/model"
	outboundproxy "github.com/fynhq/windwatch/internal/proxy"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

var (
	ErrNotStarted   = errors.New("scheduler is not started")
	ErrAlreadyStart = errors.New("scheduler is already started")
	ErrCheckRunning = errors.New("a check is already running for this monitor")
)

const (
	globalConcurrency     = 16
	resultBuffer          = 128
	rawRetention          = 7 * 24 * time.Hour
	eventRetention        = 365 * 24 * time.Hour
	halfHourlyRetention   = 365 * 24 * time.Hour
	notificationRetention = 365 * 24 * time.Hour
	certificateRetention  = 30 * 24 * time.Hour
	domainInfoRetention   = 90 * 24 * time.Hour
	domainRefreshInterval = 24 * time.Hour
	domainRetryBase       = 30 * time.Minute
)

type NotificationSink interface {
	Enqueue(notification model.Notification) bool
}

type notificationAvailability interface {
	NotificationsEnabled(ctx context.Context) (bool, error)
}

type outcome struct {
	monitorID int64
	result    model.CheckResult
	checkedAt time.Time
}

type runnerHandle struct {
	cancel context.CancelFunc
}

type Scheduler struct {
	store    *store.Store
	registry *checker.Registry
	cipher   *secret.Cipher
	notifier NotificationSink
	log      *slog.Logger

	now          func() time.Time
	initialDelay func(time.Duration) time.Duration
	nextDelay    func(time.Duration) time.Duration

	semaphore  chan struct{}
	results    chan outcome
	domainWake chan struct{}

	mu              sync.Mutex
	ctx             context.Context
	started         bool
	runners         map[int64]*runnerHandle
	inflight        map[int64]struct{}
	warningContexts map[int64]string
	wg              sync.WaitGroup
}

func (s *Scheduler) SetCipher(cipher *secret.Cipher) {
	s.cipher = cipher
}

func (s *Scheduler) SetNotifier(notifier NotificationSink) {
	s.notifier = notifier
}

func New(st *store.Store, registry *checker.Registry, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		store:    st,
		registry: registry,
		log:      logger,
		now:      time.Now,
		initialDelay: func(interval time.Duration) time.Duration {
			window := int64(interval / 10)
			if window <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(window + 1))
		},
		nextDelay: func(interval time.Duration) time.Duration {
			window := int64(interval / 10)
			if window <= 0 {
				return interval
			}
			return interval + time.Duration(rand.Int64N(window+1))
		},
		semaphore:       make(chan struct{}, globalConcurrency),
		results:         make(chan outcome, resultBuffer),
		domainWake:      make(chan struct{}, 1),
		runners:         make(map[int64]*runnerHandle),
		inflight:        make(map[int64]struct{}),
		warningContexts: make(map[int64]string),
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	monitors, err := s.store.ListMonitors(ctx)
	if err != nil {
		return fmt.Errorf("load monitors: %w", err)
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return ErrAlreadyStart
	}
	s.ctx = ctx
	s.started = true
	s.mu.Unlock()

	s.wg.Add(3)
	go func() {
		defer s.wg.Done()
		s.collect(ctx)
	}()
	go func() {
		defer s.wg.Done()
		s.maintain(ctx)
	}()
	go func() {
		defer s.wg.Done()
		s.refreshDomains(ctx)
	}()

	for _, monitor := range monitors {
		if monitor.Enabled {
			s.startRunner(monitor)
		}
	}
	return nil
}

func (s *Scheduler) Wait() {
	s.wg.Wait()
}

func (s *Scheduler) Reload(monitorID int64) error {
	runCtx, err := s.context()
	if err != nil {
		return err
	}
	s.stopRunner(monitorID)
	monitor, err := s.store.MonitorByID(runCtx, monitorID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load monitor %d: %w", monitorID, err)
	}
	if monitor.Enabled {
		s.startRunner(monitor)
	}
	if monitor.Type == model.MonitorTypeHTTP && monitor.HTTP != nil && monitor.HTTP.DomainCheck {
		select {
		case s.domainWake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (s *Scheduler) Remove(monitorID int64) {
	s.stopRunner(monitorID)
	s.setWarningContext(monitorID, "")
}

func (s *Scheduler) CheckNow(monitorID int64) error {
	runCtx, err := s.context()
	if err != nil {
		return err
	}
	s.mu.Lock()
	_, busy := s.inflight[monitorID]
	s.mu.Unlock()
	if busy {
		return ErrCheckRunning
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.execute(runCtx, monitorID, true)
	}()
	return nil
}

func (s *Scheduler) context() (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.ctx == nil || s.ctx.Err() != nil {
		return nil, ErrNotStarted
	}
	return s.ctx, nil
}

func (s *Scheduler) startRunner(monitor model.Monitor) {
	ctx, err := s.context()
	if err != nil {
		return
	}
	if monitor.State.Status == model.StatusDown {
		startedAt := monitor.State.StatusSince
		if startedAt.IsZero() {
			startedAt = s.now().UTC()
		}
		if err := s.store.EnsureOpenDownEvent(ctx, monitor.ID, startedAt, monitor.State.LastError); err != nil {
			s.log.Warn("ensure open down event", "monitor_id", monitor.ID, "error", err)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	if old := s.runners[monitor.ID]; old != nil {
		old.cancel()
	}
	s.runners[monitor.ID] = &runnerHandle{cancel: cancel}
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if monitor.Type == model.MonitorTypePush {
			s.runPushMonitor(runCtx, monitor)
			return
		}
		s.runMonitor(runCtx, monitor)
	}()
}

func (s *Scheduler) stopRunner(monitorID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if runner := s.runners[monitorID]; runner != nil {
		runner.cancel()
		delete(s.runners, monitorID)
	}
}

func (s *Scheduler) runMonitor(ctx context.Context, monitor model.Monitor) {
	timer := time.NewTimer(s.initialDelay(monitor.Interval))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.execute(ctx, monitor.ID, false)
		timer.Reset(s.nextDelay(monitor.Interval))
	}
}

func (s *Scheduler) execute(ctx context.Context, monitorID int64, manual bool) {
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	if _, exists := s.inflight[monitorID]; exists {
		s.mu.Unlock()
		return
	}
	s.inflight[monitorID] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, monitorID)
		s.mu.Unlock()
	}()

	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-ctx.Done():
		return
	}

	monitor, err := s.store.MonitorByID(ctx, monitorID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		s.log.Error("load monitor for check", "monitor_id", monitorID, "error", err)
		return
	}
	if !monitor.Enabled && !manual {
		return
	}

	if monitor.Type == model.MonitorTypePush {
		s.enqueuePushOutcome(ctx, monitor)
		return
	}

	var egress *outboundproxy.Config
	if monitor.ProxyID != nil && (monitor.Type == model.MonitorTypeHTTP || monitor.Type == model.MonitorTypeTCP) {
		egress, err = s.resolveProxy(ctx, *monitor.ProxyID)
		if err != nil {
			s.sendOutcome(ctx, monitorID, model.CheckResult{
				ErrorKind:    model.ErrorKindProxy,
				ErrorMessage: truncateErrorText(err.Error()),
				MetaJSON:     "{}",
			}, s.now().UTC())
			return
		}
	}

	checkCtx, cancel := context.WithTimeout(ctx, monitor.Timeout)
	result, err := s.registry.CheckWithProxy(checkCtx, monitor, egress)
	cancel()
	if err != nil {
		result = model.CheckResult{
			ErrorKind:    model.ErrorKindInternal,
			ErrorMessage: truncateErrorText(err.Error()),
			MetaJSON:     "{}",
		}
	}
	if ctx.Err() != nil {
		return
	}

	checkedAt := s.now().UTC()
	s.sendOutcome(ctx, monitorID, result, checkedAt)
}

func (s *Scheduler) runPushMonitor(ctx context.Context, monitor model.Monitor) {
	tick := monitor.Interval
	if tick > time.Minute {
		tick = time.Minute
	}
	timer := time.NewTimer(s.initialDelay(tick))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.evaluatePushMonitor(ctx, monitor.ID)
		timer.Reset(tick)
	}
}

func (s *Scheduler) evaluatePushMonitor(ctx context.Context, monitorID int64) {
	monitor, err := s.store.MonitorByID(ctx, monitorID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil || !monitor.Enabled || monitor.Type != model.MonitorTypePush {
		return
	}
	s.enqueuePushOutcome(ctx, monitor)
}

func (s *Scheduler) enqueuePushOutcome(ctx context.Context, monitor model.Monitor) {
	token, err := s.store.PushTokenByMonitorID(ctx, monitor.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && token.LastHeartbeat.IsZero()) {
		return
	}
	if err != nil {
		s.log.Error("load push token", "monitor_id", monitor.ID, "error", err)
		return
	}

	now := s.now().UTC()
	fresh := now.Sub(token.LastHeartbeat) <= monitor.Interval+monitor.Timeout
	result := model.CheckResult{
		Success:  fresh,
		Latency:  0,
		MetaJSON: fmt.Sprintf(`{"last_heartbeat_ms":%d}`, token.LastHeartbeat.UnixMilli()),
	}
	if !fresh {
		result.ErrorKind = model.ErrorKindHeartbeatTimeout
		result.ErrorMessage = fmt.Sprintf("no heartbeat since %s", token.LastHeartbeat.Format(time.RFC3339))
	}
	s.sendOutcome(ctx, monitor.ID, result, now)
}

func (s *Scheduler) sendOutcome(ctx context.Context, monitorID int64, result model.CheckResult, checkedAt time.Time) {
	select {
	case s.results <- outcome{monitorID: monitorID, result: result, checkedAt: checkedAt}:
	case <-ctx.Done():
	}
}

func (s *Scheduler) resolveProxy(ctx context.Context, proxyID int64) (*outboundproxy.Config, error) {
	stored, err := s.store.ProxyByID(ctx, proxyID)
	if err != nil {
		return nil, fmt.Errorf("load proxy %d: %w", proxyID, err)
	}
	password := ""
	if stored.PasswordEnc != "" {
		if s.cipher == nil {
			return nil, fmt.Errorf("secret key is not initialized")
		}
		password, err = s.cipher.Decrypt(stored.PasswordEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt proxy password: %w", err)
		}
	}
	config := &outboundproxy.Config{
		Type:     stored.Type,
		Address:  stored.Address,
		Username: stored.Username,
		Password: password,
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return config, nil
}

func (s *Scheduler) collect(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.results:
			s.apply(ctx, job)
		}
	}
}

func (s *Scheduler) apply(ctx context.Context, job outcome) {
	monitor, err := s.store.MonitorByID(ctx, job.monitorID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		s.log.Error("load monitor for result", "monitor_id", job.monitorID, "error", err)
		return
	}

	previousStatus := monitor.State.Status
	previousSince := monitor.State.StatusSince
	downStartedAt := time.Time{}
	if !job.result.Success && previousStatus != model.StatusDown &&
		monitor.State.ConsecutiveFailures+1 >= failedThreshold(monitor) {
		downStartedAt = s.firstFailureStart(ctx, monitor, job.checkedAt)
	}
	aux := s.auxiliaryWarning(ctx, monitor, job.result)
	warning := job.result.Success && aux.active
	warningKey := ""
	if warning {
		warningKey = warningReminderContext(monitor, aux, job.result)
	}
	nextState, event, closeDown := advance(monitor, monitor.State, job.result, job.checkedAt)
	if event != nil && event.Type == model.EventDown && !downStartedAt.IsZero() {
		event.StartedAt = downStartedAt
	}
	if nextState.Status != model.StatusDown && job.result.Success {
		switch {
		case warning && nextState.Status != model.StatusWarning:
			if previousStatus == model.StatusWarning {
				nextState.StatusSince = previousSince
			} else {
				nextState.StatusSince = job.checkedAt
			}
			nextState.Status = model.StatusWarning
		case !warning && nextState.Status == model.StatusWarning:
			nextState.Status = model.StatusUp
			nextState.StatusSince = job.checkedAt
		}
	}

	checkStatus := model.CheckDown
	if job.result.Success {
		checkStatus = model.CheckUp
		if warning {
			checkStatus = model.CheckWarning
		}
	}
	check := model.Check{
		MonitorID:    monitor.ID,
		CheckedAt:    job.checkedAt,
		Status:       checkStatus,
		Latency:      job.result.Latency,
		HTTPStatus:   job.result.HTTPStatus,
		ErrorKind:    job.result.ErrorKind,
		ErrorMessage: job.result.ErrorMessage,
		MetaJSON:     job.result.MetaJSON,
	}
	if job.result.Certificate != nil {
		check.CertFingerprint = job.result.Certificate.Fingerprint
		if err := s.store.UpsertCertificate(ctx, *job.result.Certificate); err != nil {
			s.log.Warn("store certificate", "monitor_id", monitor.ID, "error", err)
		}
	}
	if event != nil && event.Type == model.EventDown {
		event.Message = job.result.ErrorMessage
		event.MetaJSON = "{}"
	}
	outageStart := time.Time{}
	if event != nil && event.Type == model.EventUp && previousStatus == model.StatusDown {
		outageStart = s.outageStart(ctx, monitor.ID, previousSince, job.checkedAt)
	}
	if err := s.store.RecordCheck(ctx, check, nextState, event, closeDown); err != nil {
		s.log.Error("record check", "monitor_id", monitor.ID, "error", err)
		return
	}
	if event != nil {
		s.log.Info("monitor state changed",
			"monitor_id", monitor.ID,
			"name", monitor.Name,
			"previous", previousStatus,
			"status", nextState.Status,
		)
		if monitor.Enabled && monitorNotificationsEnabled(monitor) && s.notifier != nil && s.notificationsAvailable(ctx) {
			notification := model.Notification{
				Key:           fmt.Sprintf("%s:%d:%d", event.Type, monitor.ID, job.checkedAt.UnixMilli()),
				Kind:          event.Type,
				MonitorID:     monitor.ID,
				MonitorName:   monitor.Name,
				MonitorTarget: monitor.Target(),
				MonitorType:   monitor.Type,
				Status:        nextState.Status,
				Message:       job.result.ErrorMessage,
				OccurredAt:    job.checkedAt,
			}
			if event.Type == model.EventUp && previousStatus == model.StatusDown {
				if outageStart.IsZero() {
					outageStart = previousSince
				}
				if !outageStart.IsZero() && !outageStart.After(job.checkedAt) {
					notification.DowntimeStart = outageStart
					notification.Downtime = job.checkedAt.Sub(outageStart)
					notification.DowntimeCycles = outageCycles(notification.Downtime, monitor.Interval, monitor.Type)
				}
			}
			s.notifier.Enqueue(notification)
		}
	}
	previousWarningKey := s.warningContext(monitor.ID)
	warningChanged := warning && (previousStatus != model.StatusWarning || previousWarningKey != warningKey)
	if warningChanged && monitor.Enabled && monitorNotificationsEnabled(monitor) && s.notifier != nil && s.notificationsAvailable(ctx) {
		notification := s.warningNotification(monitor, job.result, aux, nextState, job.checkedAt)
		if s.notifier.Enqueue(notification) {
			s.setWarningContext(monitor.ID, warningKey)
		}
	} else if !warning {
		s.setWarningContext(monitor.ID, "")
	}
	s.processReminders(ctx, monitor, job.result, aux, warning, nextState, previousStatus, job.checkedAt)
}

func monitorNotificationsEnabled(monitor model.Monitor) bool {
	return monitor.NotifyEnabled && len(monitor.EnabledNotificationChannelIDs) > 0
}

func failedThreshold(monitor model.Monitor) int {
	threshold := monitor.FailThreshold
	if threshold < 1 {
		threshold = 1
	}
	return threshold
}

// firstFailureStart walks back through the failures that lead to the DOWN
// transition so the outage starts at the first failed check, not at the later
// threshold confirmation. ConsecutiveFailures is bounded by fail_threshold
// here, so the query reads at most 19 rows.
func (s *Scheduler) firstFailureStart(ctx context.Context, monitor model.Monitor, at time.Time) time.Time {
	prior := monitor.State.ConsecutiveFailures
	if prior <= 0 {
		return at
	}
	checks, err := s.store.RecentChecks(ctx, monitor.ID, prior)
	if err != nil {
		s.log.Warn("load checks for outage start", "monitor_id", monitor.ID, "error", err)
		return at
	}
	if len(checks) != prior {
		return at
	}
	start := checks[len(checks)-1].CheckedAt
	if start.IsZero() || start.After(at) {
		return at
	}
	for _, check := range checks {
		if check.Status != model.CheckDown {
			return at
		}
	}
	return start
}

func (s *Scheduler) outageStart(ctx context.Context, monitorID int64, fallback, at time.Time) time.Time {
	start, ok, err := s.store.OpenDownEventStart(ctx, monitorID)
	if err != nil {
		s.log.Warn("load open down event start", "monitor_id", monitorID, "error", err)
		return fallback
	}
	if !ok || start.IsZero() || start.After(at) {
		return fallback
	}
	return start
}

func outageCycles(downtime, interval time.Duration, monitorType model.MonitorType) int {
	if downtime <= 0 || interval <= 0 {
		return 0
	}
	if monitorType == model.MonitorTypePush && interval > time.Minute {
		interval = time.Minute
	}
	cycles := int((downtime + interval/2) / interval)
	if cycles < 1 {
		cycles = 1
	}
	return cycles
}

func (s *Scheduler) notificationsAvailable(ctx context.Context) bool {
	provider, ok := s.notifier.(notificationAvailability)
	if !ok {
		return true
	}
	enabled, err := provider.NotificationsEnabled(ctx)
	if err != nil {
		s.log.Warn("check notification availability", "error", err)
		return false
	}
	return enabled
}

func (s *Scheduler) warningContext(monitorID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.warningContexts[monitorID]
}

func (s *Scheduler) setWarningContext(monitorID int64, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value == "" {
		delete(s.warningContexts, monitorID)
		return
	}
	s.warningContexts[monitorID] = value
}

func truncateErrorText(value string) string {
	const limit = 512
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

type auxWarning struct {
	active     bool
	cert       bool
	domain     bool
	domainName string
	domainInfo *model.DomainInfo
}

func (s *Scheduler) auxiliaryWarning(ctx context.Context, monitor model.Monitor, result model.CheckResult) auxWarning {
	if !result.Success || monitor.HTTP == nil {
		return auxWarning{}
	}
	var warning auxWarning
	cfg := monitor.HTTP
	if cfg.CertCheck && result.Certificate != nil && !result.Certificate.NotAfter.IsZero() {
		days := int(time.Until(result.Certificate.NotAfter).Hours() / 24)
		if days <= cfg.CertWarnDays {
			warning.active = true
			warning.cert = true
		}
	}
	if cfg.DomainCheck {
		domain, err := domains.DomainForURL(cfg.URL)
		if err == nil {
			if info, err := s.store.DomainInfo(ctx, domain); err == nil {
				warning.domainName = domain
				warning.domainInfo = &info
				if !info.ExpiresAt.IsZero() {
					days := int(time.Until(info.ExpiresAt).Hours() / 24)
					if days <= cfg.DomainWarnDays {
						warning.active = true
						warning.domain = true
					}
				}
				if domains.HasSuspensionStatus(info.Statuses) {
					warning.active = true
					warning.domain = true
				}
			}
		}
	}
	return warning
}

func (s *Scheduler) warningNotification(monitor model.Monitor, result model.CheckResult, warning auxWarning, nextState model.MonitorState, at time.Time) model.Notification {
	notification := model.Notification{
		Kind:          model.EventWarnDomain,
		MonitorID:     monitor.ID,
		MonitorName:   monitor.Name,
		MonitorTarget: monitor.Target(),
		MonitorType:   monitor.Type,
		Status:        nextState.Status,
		OccurredAt:    at,
		Cert:          result.Certificate,
		Domain:        warning.domainInfo,
	}
	if warning.cert {
		notification.Kind = model.EventWarnCert
		fingerprint := ""
		if result.Certificate != nil {
			fingerprint = result.Certificate.Fingerprint
		}
		notification.Key = fmt.Sprintf("warn_cert:%d:%s:%d", monitor.ID, fingerprint, monitor.HTTP.CertWarnDays)
		return notification
	}
	expires := int64(0)
	if warning.domainInfo != nil && !warning.domainInfo.ExpiresAt.IsZero() {
		expires = warning.domainInfo.ExpiresAt.UnixMilli()
	}
	notification.Key = fmt.Sprintf("warn_domain:%s:%d:%d", warning.domainName, expires, monitor.HTTP.DomainWarnDays)
	return notification
}

func (s *Scheduler) refreshDomains(ctx context.Context) {
	retryDelay := domainRetryBase
	nextDelay := func(retry bool) time.Duration {
		if !retry {
			retryDelay = domainRetryBase
			return domainRefreshInterval
		}
		delay := retryDelay
		retryDelay = min(retryDelay*2, domainRefreshInterval)
		return delay
	}
	refresh := func() time.Duration {
		monitors, err := s.store.ListMonitors(ctx)
		if err != nil {
			s.log.Warn("list monitors for domain refresh", "error", err)
			return nextDelay(true)
		}
		resolver := func(proxyID *int64) (*outboundproxy.Config, error) {
			if proxyID == nil {
				return nil, nil
			}
			proxyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return s.resolveProxy(proxyCtx, *proxyID)
		}
		retry, err := domains.Refresh(ctx, s.store, monitors, resolver, s.now().UTC())
		if err != nil {
			s.log.Warn("refresh domains", "error", err)
		}
		return nextDelay(retry)
	}

	timer := time.NewTimer(refresh())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.domainWake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		timer.Reset(refresh())
	}
}

func (s *Scheduler) maintain(ctx context.Context) {
	aggregate := func(now time.Time) error {
		untilHalfHour := now.Truncate(30 * time.Minute)
		sinceHalfHour := now.Add(-rawRetention).Truncate(30 * time.Minute)
		if last, ok, err := s.store.LastRollupStart(ctx); err != nil {
			return err
		} else if ok {
			sinceHalfHour = last.Add(-30 * time.Minute)
			minSince := now.Add(-rawRetention).Truncate(30 * time.Minute)
			if sinceHalfHour.Before(minSince) {
				sinceHalfHour = minSince
			}
		}
		if untilHalfHour.After(sinceHalfHour) {
			if err := s.store.AggregateHalfHourlyRollups(ctx, sinceHalfHour, untilHalfHour, now); err != nil {
				return err
			}
		}
		if err := s.store.DeleteRollupsBefore(ctx, untilHalfHour.Add(-halfHourlyRetention)); err != nil {
			return fmt.Errorf("delete old half-hourly rollups: %w", err)
		}
		return nil
	}
	cleanup := func() {
		now := s.now().UTC()
		if err := s.store.DeleteExpiredSessions(ctx, now); err != nil {
			s.log.Warn("delete expired sessions", "error", err)
		}
		if err := s.store.DeleteNotificationsBefore(ctx, now.Add(-notificationRetention)); err != nil {
			s.log.Warn("delete old notifications", "error", err)
		}
		if err := s.store.DeleteUnreferencedCertificates(ctx, now.Add(-certificateRetention)); err != nil {
			s.log.Warn("delete unreferenced certificates", "error", err)
		}
		if err := s.store.DeleteDomainInfoBefore(ctx, now.Add(-domainInfoRetention)); err != nil {
			s.log.Warn("delete old domain info", "error", err)
		}
		if err := aggregate(now); err != nil {
			s.log.Error("aggregate rollups", "error", err)
			return
		}
		if err := s.store.DeleteChecksBefore(ctx, now.Add(-rawRetention)); err != nil {
			s.log.Warn("delete old checks", "error", err)
		}
		if err := s.store.DeleteEventsBefore(ctx, now.Add(-eventRetention)); err != nil {
			s.log.Warn("delete old events", "error", err)
		}
	}
	cleanup()

	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}
