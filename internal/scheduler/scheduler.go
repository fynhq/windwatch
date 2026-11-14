package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"time"

	"github.com/fynhq/windwatch/internal/checker"
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
	rollupInterval        = 5 * time.Minute
	failureRecheckDelay   = 5 * time.Second
	failureRecheckTimeout = 10 * time.Second
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
	// failureStartedAt is the first failed probe of a recheck cycle. The DOWN
	// event must start there even though the failure is confirmed later.
	failureStartedAt time.Time
	// manual results may be applied even for a paused monitor: the web layer
	// rejects such requests, but the scheduler API keeps its historical
	// behavior for tests and internal callers.
	manual bool
	// generation identifies the monitor configuration that produced this
	// result. A reload or pause/resume invalidates in-flight outcomes so a
	// stale probe cannot advance state after the user changed the monitor.
	generation uint64
	versioned  bool
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

	recheckDelay   time.Duration
	recheckTimeout time.Duration

	semaphore chan struct{}
	results   chan outcome

	mu          sync.Mutex
	ctx         context.Context
	started     bool
	stopping    bool
	runners     map[int64]*runnerHandle
	inflight    map[int64]struct{}
	generations map[int64]uint64
	wg          sync.WaitGroup
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
		recheckDelay:   failureRecheckDelay,
		recheckTimeout: failureRecheckTimeout,
		semaphore:      make(chan struct{}, globalConcurrency),
		results:        make(chan outcome, resultBuffer),
		runners:        make(map[int64]*runnerHandle),
		inflight:       make(map[int64]struct{}),
		generations:    make(map[int64]uint64),
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

	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		defer s.recoverTask("collector")
		s.collect(ctx)
	}()
	go func() {
		defer s.wg.Done()
		defer s.recoverTask("maintenance")
		s.maintain(ctx)
	}()

	for _, monitor := range monitors {
		if monitor.Enabled {
			s.startRunner(monitor)
		}
	}
	return nil
}

func (s *Scheduler) Wait() {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	s.wg.Wait()
}

// recoverTask keeps a panic in one monitor or maintenance cycle from taking
// down the whole process. Long-lived loops wrap each iteration, not just the
// goroutine, so the task can continue after one bad cycle.
func (s *Scheduler) recoverTask(task string) {
	if recovered := recover(); recovered != nil {
		s.log.Error("background task panic", "task", task, "panic", recovered, "stack", string(debug.Stack()))
	}
}

func (s *Scheduler) executeSafely(ctx context.Context, monitorID int64, manual bool) {
	defer s.recoverTask("check")
	s.execute(ctx, monitorID, manual)
}

func (s *Scheduler) applySafely(ctx context.Context, job outcome) {
	defer s.recoverTask("collector-apply")
	s.apply(ctx, job)
}

func (s *Scheduler) Reload(monitorID int64) error {
	runCtx, err := s.context()
	if err != nil {
		return err
	}
	// Load before stopping the old runner. A transient read failure must not
	// leave an otherwise healthy monitor unscheduled.
	monitor, err := s.store.MonitorByID(runCtx, monitorID)
	if errors.Is(err, store.ErrNotFound) {
		s.stopRunner(monitorID)
		s.mu.Lock()
		delete(s.generations, monitorID)
		s.mu.Unlock()
		return nil
	}
	if err != nil {
		return fmt.Errorf("load monitor %d: %w", monitorID, err)
	}
	s.bumpGeneration(monitorID)
	s.stopRunner(monitorID)
	if monitor.Enabled {
		s.startRunner(monitor)
	}
	return nil
}

func (s *Scheduler) Remove(monitorID int64) {
	s.stopRunner(monitorID)
	s.mu.Lock()
	delete(s.generations, monitorID)
	s.mu.Unlock()
}

// SetMonitorEnabled applies pause/resume semantics in one place so the web layer
// does not orchestrate state transitions. It stops or starts the runner, clears
// reminder state on pause, closes any open DOWN event, and resets threshold
// counters so paused time does not count toward warnings or outages.
func (s *Scheduler) SetMonitorEnabled(ctx context.Context, monitorID int64, enabled bool) error {
	// Invalidate in-flight results before the state change so a probe that
	// started before the user pressed pause cannot advance the monitor later.
	s.bumpGeneration(monitorID)
	now := s.now().UTC()
	if err := s.store.SetMonitorEnabledWithReset(ctx, monitorID, enabled, now); err != nil {
		return fmt.Errorf("set monitor enabled: %w", err)
	}
	if err := s.Reload(monitorID); err != nil {
		// Reload is no longer destructive on failure: the old runner remains
		// active and execute() re-reads the enabled flag every cycle.
		s.log.Warn("reload scheduler after toggle", "monitor_id", monitorID, "error", err)
	}
	return nil
}

func (s *Scheduler) CheckNow(monitorID int64) error {
	s.mu.Lock()
	if !s.started || s.stopping || s.ctx == nil || s.ctx.Err() != nil {
		s.mu.Unlock()
		return ErrNotStarted
	}
	if _, busy := s.inflight[monitorID]; busy {
		s.mu.Unlock()
		return ErrCheckRunning
	}
	runCtx := s.ctx
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		s.executeSafely(runCtx, monitorID, true)
	}()
	return nil
}

func (s *Scheduler) context() (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopping || s.ctx == nil || s.ctx.Err() != nil {
		return nil, ErrNotStarted
	}
	return s.ctx, nil
}

func (s *Scheduler) bumpGeneration(monitorID int64) {
	s.mu.Lock()
	s.generations[monitorID]++
	s.mu.Unlock()
}

func (s *Scheduler) generationCurrent(monitorID int64, generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generations[monitorID] == generation
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
	if s.stopping {
		s.mu.Unlock()
		cancel()
		return
	}
	if old := s.runners[monitor.ID]; old != nil {
		old.cancel()
	}
	s.runners[monitor.ID] = &runnerHandle{cancel: cancel}
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer s.recoverTask("runner")
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
		s.executeSafely(ctx, monitor.ID, false)
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
	generation := s.generations[monitorID]
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, monitorID)
		s.mu.Unlock()
	}()

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
		s.enqueuePushOutcomeVersioned(ctx, monitor, manual, generation, true)
		return
	}

	var egress *outboundproxy.Config
	if monitor.ProxyID != nil && (monitor.Type == model.MonitorTypeHTTP || monitor.Type == model.MonitorTypeTCP) {
		egress, err = s.resolveProxy(ctx, *monitor.ProxyID)
		if err != nil {
			s.sendOutcome(ctx, outcome{
				monitorID: monitorID,
				result: model.CheckResult{
					ErrorKind:    model.ErrorKindProxy,
					ErrorMessage: truncateErrorText(err.Error()),
					MetaJSON:     "{}",
				},
				checkedAt:  s.now().UTC(),
				manual:     manual,
				generation: generation,
				versioned:  true,
			})
			return
		}
	}

	startedAt := s.now().UTC()
	result, ok := s.probe(ctx, monitor, egress, monitor.Timeout)
	if !ok {
		return
	}

	failureStartedAt := time.Time{}
	if !result.Success {
		// The collector may have applied an earlier result while this probe was
		// running. Reload the state before deciding whether the failure would
		// cross the threshold.
		if latest, loadErr := s.store.MonitorByID(ctx, monitorID); loadErr == nil {
			monitor.State = latest.State
		}
	}
	if s.shouldRecheck(monitor, result) {
		firstFailure := result
		result, ok = s.recheckFailure(ctx, monitor, egress, result)
		if !ok {
			return
		}
		if result.Success {
			s.log.Info("failure cleared by recheck",
				"monitor_id", monitor.ID,
				"name", monitor.Name,
				"error_kind", firstFailure.ErrorKind,
			)
		} else {
			failureStartedAt = startedAt
			s.log.Info("failure confirmed after recheck",
				"monitor_id", monitor.ID,
				"name", monitor.Name,
				"error_kind", firstFailure.ErrorKind,
			)
		}
	}

	s.sendOutcome(ctx, outcome{
		monitorID:        monitorID,
		result:           result,
		checkedAt:        s.now().UTC(),
		failureStartedAt: failureStartedAt,
		manual:           manual,
		generation:       generation,
		versioned:        true,
	})
}

// probe runs one check attempt under the global concurrency limit. Each
// attempt acquires and releases the semaphore separately so the delay before a
// recheck does not hold a slot.
func (s *Scheduler) probe(ctx context.Context, monitor model.Monitor, egress *outboundproxy.Config, timeout time.Duration) (model.CheckResult, bool) {
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-ctx.Done():
		return model.CheckResult{}, false
	}

	checkCtx, cancel := context.WithTimeout(ctx, timeout)
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
		return model.CheckResult{}, false
	}
	return result, true
}

// shouldRecheck reports whether a failed probe is about to cross the failure
// threshold. Configuration and internal errors cannot be fixed by probing
// again, so they keep the original immediate behavior.
func (s *Scheduler) shouldRecheck(monitor model.Monitor, result model.CheckResult) bool {
	if result.Success || monitor.Type == model.MonitorTypePush {
		return false
	}
	if monitor.State.Status == model.StatusDown {
		return false
	}
	if monitor.State.ConsecutiveFailures+1 < failedThreshold(monitor) {
		return false
	}
	switch result.ErrorKind {
	case model.ErrorKindInvalidConfig, model.ErrorKindInternal:
		return false
	}
	return true
}

// recheckFailure probes one more time before a monitor enters DOWN. A single
// transient network or TLS hiccup is common, so a successful recheck clears
// the failure for this cycle; if the recheck also fails the first failure
// remains the canonical result. The recheck uses a shorter timeout because the
// first full-timeout probe already established that the target is failing.
func (s *Scheduler) recheckFailure(ctx context.Context, monitor model.Monitor, egress *outboundproxy.Config, first model.CheckResult) (model.CheckResult, bool) {
	if delay := s.recheckDelay; delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return first, false
		case <-timer.C:
		}
	}
	timeout := monitor.Timeout
	if s.recheckTimeout > 0 && timeout > s.recheckTimeout {
		timeout = s.recheckTimeout
	}
	result, ok := s.probe(ctx, monitor, egress, timeout)
	if !ok {
		return first, false
	}
	if result.Success {
		return result, true
	}
	return first, true
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
		s.executeSafely(ctx, monitor.ID, false)
		timer.Reset(tick)
	}
}

func (s *Scheduler) enqueuePushOutcome(ctx context.Context, monitor model.Monitor, manual bool) {
	s.enqueuePushOutcomeVersioned(ctx, monitor, manual, 0, false)
}

func (s *Scheduler) enqueuePushOutcomeVersioned(ctx context.Context, monitor model.Monitor, manual bool, generation uint64, versioned bool) {
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
	s.sendOutcome(ctx, outcome{
		monitorID:  monitor.ID,
		result:     result,
		checkedAt:  now,
		manual:     manual,
		generation: generation,
		versioned:  versioned,
	})
}

func (s *Scheduler) sendOutcome(ctx context.Context, job outcome) {
	select {
	case s.results <- job:
		return
	case <-ctx.Done():
		// The probe may have completed just before cancellation. Try once more
		// so the collector drain can still persist it.
		select {
		case s.results <- job:
		default:
		}
	}
}

func (s *Scheduler) resolveProxy(ctx context.Context, proxyID int64) (*outboundproxy.Config, error) {
	stored, err := s.store.ProxyByID(ctx, proxyID)
	if err != nil {
		return nil, fmt.Errorf("load proxy %d: %w", proxyID, err)
	}
	var decrypt func(string) (string, error)
	if s.cipher != nil {
		decrypt = s.cipher.Decrypt
	}
	config, err := outboundproxy.FromStored(stored, decrypt)
	if err != nil {
		return nil, fmt.Errorf("load proxy %d: %w", proxyID, err)
	}
	return config, nil
}

func (s *Scheduler) collect(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// Drain results already queued before cancellation. Probes that
			// are still in flight observe the canceled context and are not
			// recorded; this only guarantees completed work is not lost.
			for {
				select {
				case job := <-s.results:
					s.applySafely(context.Background(), job)
				default:
					return
				}
			}
		case job := <-s.results:
			s.applySafely(ctx, job)
		}
	}
}

func (s *Scheduler) apply(ctx context.Context, job outcome) {
	if job.versioned && !s.generationCurrent(job.monitorID, job.generation) {
		s.log.Debug("discarding stale check result", "monitor_id", job.monitorID, "generation", job.generation)
		return
	}
	monitor, err := s.store.MonitorByID(ctx, job.monitorID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		s.log.Error("load monitor for result", "monitor_id", job.monitorID, "error", err)
		return
	}
	if !monitor.Enabled && !job.manual {
		// A scheduled result can finish after the user pauses a monitor. Do not
		// let that late result reopen a DOWN event or advance state while paused.
		// Manual/internal results are still applied for compatibility; the web
		// layer rejects manual checks on paused monitors, and versioned outcomes
		// are discarded above when the pause changed the generation.
		s.log.Debug("discarding result for paused monitor", "monitor_id", monitor.ID)
		return
	}

	previousStatus := monitor.State.Status
	previousSince := monitor.State.StatusSince
	previousWarningKey := monitor.State.WarningContext
	downStartedAt := time.Time{}
	if !job.result.Success && previousStatus != model.StatusDown &&
		monitor.State.ConsecutiveFailures+1 >= failedThreshold(monitor) {
		downStartedAt = s.firstFailureStart(ctx, monitor, job.checkedAt)
		if !job.failureStartedAt.IsZero() &&
			(downStartedAt.IsZero() || job.failureStartedAt.Before(downStartedAt)) {
			downStartedAt = job.failureStartedAt
		}
	}
	aux := s.auxiliaryWarning(ctx, monitor, job.result)
	warning := job.result.Success && aux.active
	warningKey := ""
	if warning {
		warningKey = warningReminderContext(monitor, job.result)
	}
	nextState, event, closeDown := advance(monitor, monitor.State, job.result, job.checkedAt)
	if event != nil && event.Type == model.EventDown && !downStartedAt.IsZero() {
		event.StartedAt = downStartedAt
	}
	if nextState.Status != model.StatusDown && job.result.Success {
		if warning && nextState.Status != model.StatusWarning {
			if previousStatus == model.StatusWarning && previousWarningKey == warningKey {
				nextState.StatusSince = previousSince
			} else {
				nextState.StatusSince = job.checkedAt
			}
			nextState.Status = model.StatusWarning
		}
	}

	checkStatus := model.CheckDown
	if job.result.Success {
		checkStatus = model.CheckUp
		if warning && nextState.Status != model.StatusDown {
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

	warningChanged := warning && nextState.Status == model.StatusWarning &&
		(previousStatus != model.StatusWarning || previousWarningKey != warningKey)
	notificationReady := false
	if event != nil || warningChanged {
		notificationReady = monitor.Enabled && monitorNotificationsEnabled(monitor) &&
			s.notifier != nil && s.notificationsAvailable(ctx)
	}
	notifications := make([]model.Notification, 0, 2)
	if event != nil && notificationReady {
		notification := model.Notification{
			Key:           model.StateNotificationKey(string(event.Type), monitor.ID, job.checkedAt),
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
		notifications = append(notifications, notification)
	}
	if warningChanged && notificationReady {
		notifications = append(notifications, s.warningNotification(monitor, job.result, nextState, job.checkedAt))
	}

	// Persist the warning episode independently of whether a notification
	// channel is currently available. Otherwise status_since would advance on
	// every successful check while notifications are disabled.
	var warningContext *string
	if nextState.Status == model.StatusWarning && warning {
		value := warningKey
		warningContext = &value
	} else if nextState.Status != model.StatusWarning && previousWarningKey != "" {
		value := ""
		warningContext = &value
	}

	if err := s.store.RecordCheckWithNotifications(ctx, check, nextState, event, closeDown, notifications, warningContext); err != nil {
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
	}
	s.wakeNotifications(notifications)

	s.processReminders(ctx, monitor, job.result, warning, nextState, previousStatus, job.checkedAt)
}

func (s *Scheduler) wakeNotifications(notifications []model.Notification) {
	if s.notifier == nil || len(notifications) == 0 {
		return
	}
	if waker, ok := s.notifier.(interface{ Wake() }); ok {
		waker.Wake()
		return
	}
	// Test sinks implement only Enqueue; the outbox insert is idempotent for
	// production but keeps the fake sink observable in tests.
	for _, notification := range notifications {
		if !s.notifier.Enqueue(notification) {
			s.log.Warn("wake notification outbox", "monitor_id", notification.MonitorID, "kind", notification.Kind)
		}
	}
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
	checks, err := s.store.RecentCheckSummaries(ctx, monitor.ID, prior)
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

func rollupStart(now, last time.Time, hasLast bool) time.Time {
	if hasLast {
		return last.Add(-30 * time.Minute)
	}
	return now.Add(-rawRetention).Truncate(30 * time.Minute)
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

func truncateErrorText(value string) string {
	return model.TruncateText(value, 512)
}

type auxWarning struct {
	active bool
}

func (s *Scheduler) auxiliaryWarning(_ context.Context, monitor model.Monitor, result model.CheckResult) auxWarning {
	if !result.Success || monitor.HTTP == nil {
		return auxWarning{}
	}
	cfg := monitor.HTTP
	if cfg.CertCheck && result.Certificate != nil && !result.Certificate.NotAfter.IsZero() {
		days := int(time.Until(result.Certificate.NotAfter).Hours() / 24)
		if days <= cfg.CertWarnDays {
			return auxWarning{active: true}
		}
	}
	return auxWarning{}
}

func (s *Scheduler) warningNotification(monitor model.Monitor, result model.CheckResult, nextState model.MonitorState, at time.Time) model.Notification {
	fingerprint := ""
	if result.Certificate != nil {
		fingerprint = result.Certificate.Fingerprint
	}
	return model.Notification{
		Kind:          model.EventWarnCert,
		MonitorID:     monitor.ID,
		MonitorName:   monitor.Name,
		MonitorTarget: monitor.Target(),
		MonitorType:   monitor.Type,
		Status:        nextState.Status,
		OccurredAt:    at,
		Cert:          result.Certificate,
		// Include the episode start so a warning that clears and later recurs
		// with the same certificate context is treated as a new event.
		Key: model.WarnCertNotificationKey(monitor.ID, fingerprint, monitor.HTTP.CertWarnDays, nextState.StatusSince),
	}
}

func (s *Scheduler) maintain(ctx context.Context) {
	aggregate := func(now time.Time) error {
		untilHalfHour := now.Truncate(30 * time.Minute)
		sinceHalfHour := rollupStart(now, time.Time{}, false)
		if last, ok, err := s.store.LastRollupStart(ctx); err != nil {
			return err
		} else if ok {
			sinceHalfHour = rollupStart(now, last, true)
		}
		if untilHalfHour.After(sinceHalfHour) {
			if err := s.store.AggregateHalfHourlyRollups(ctx, sinceHalfHour, untilHalfHour, now); err != nil {
				return err
			}
		}
		return nil
	}
	pruneRollups := func(now time.Time) error {
		untilHalfHour := now.Truncate(30 * time.Minute)
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
		if err := aggregate(now); err != nil {
			s.log.Error("aggregate rollups", "error", err)
			return
		}
		if err := pruneRollups(now); err != nil {
			s.log.Error("prune rollups", "error", err)
			return
		}
		if err := s.store.DeleteChecksBefore(ctx, now.Add(-rawRetention)); err != nil {
			s.log.Warn("delete old checks", "error", err)
		}
		if err := s.store.DeleteEventsBefore(ctx, now.Add(-eventRetention)); err != nil {
			s.log.Warn("delete old events", "error", err)
		}
		if err := s.store.CheckpointWAL(ctx); err != nil {
			s.log.Warn("checkpoint wal", "error", err)
		}
		// Large deletes and aggregate scans leave enough garbage that the Go
		// scavenger can keep the process well above its live set for hours.
		// Reclaim once per maintenance cycle; this is far too expensive to do
		// after ordinary requests.
		debug.FreeOSMemory()
	}
	cleanupSafely := func() {
		defer s.recoverTask("maintenance")
		cleanup()
	}
	aggregateSafely := func() {
		defer s.recoverTask("rollup")
		if err := aggregate(s.now().UTC()); err != nil {
			s.log.Error("aggregate rollups", "error", err)
		}
	}
	cleanupSafely()

	rollupTicker := time.NewTicker(rollupInterval)
	defer rollupTicker.Stop()
	cleanupTicker := time.NewTicker(6 * time.Hour)
	defer cleanupTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-rollupTicker.C:
			aggregateSafely()
		case <-cleanupTicker.C:
			cleanupSafely()
		}
	}
}
