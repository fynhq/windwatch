package notify

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

const (
	pendingMaxAge         = 24 * time.Hour
	pendingRecoveryCap    = 200
	pendingRecoveryBudget = 2 * time.Minute
	// A blocked row is pushed out of the current retry batch so a chain of
	// ordered state notifications on one broken target cannot keep refilling
	// the batch and starve due rows for other monitors and channels.
	blockedRetryDefer = 30 * time.Second
	retryConcurrency  = 4
)

func defaultRetryDelay(completed int) time.Duration {
	switch {
	case completed <= 6:
		return 30 * time.Second
	case completed <= 16:
		return 2 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func (s *Service) recoverPendingEntries(ctx context.Context, entries []model.NotificationLog) {
	recoverCtx, cancel := context.WithTimeout(ctx, pendingRecoveryBudget)
	defer cancel()
	if len(entries) == 0 {
		return
	}
	recovered := 0
	for _, entry := range entries {
		if recoverCtx.Err() != nil {
			s.log.Warn("pending notification recovery stopped", "recovered", recovered, "remaining", len(entries)-recovered)
			return
		}
		if s.blockedByEarlierState(recoverCtx, entry) {
			continue
		}
		if s.expired(entry, time.Now().UTC()) {
			s.failExpired(recoverCtx, entry, "pending notification expired")
			continue
		}
		if entry.Attempts > 0 && !entry.NextAttemptAt.IsZero() && entry.NextAttemptAt.After(time.Now().UTC()) {
			// A restart must not bypass a 429/backoff already scheduled for
			// this row. The retry loop owns it once it becomes due. An initial
			// claim (attempts == 0) still uses the lease only to avoid a
			// concurrent sender, so startup recovery may resume it.
			continue
		}
		s.log.Info("resending pending notification", "channel", entry.Channel, "channel_id", entry.ChannelID, "target", entry.Target, "dedupe_key", entry.DedupeKey, "created_at", entry.CreatedAt.UTC().Format(time.RFC3339))
		if err := s.attemptEntry(recoverCtx, entry); err != nil {
			if recoverCtx.Err() != nil {
				s.log.Warn("pending notification recovery stopped", "recovered", recovered, "remaining", len(entries)-recovered)
				return
			}
			s.log.Warn("recovery send failed", "channel", entry.Channel, "target", entry.Target, "error", err)
			s.recordFailure(recoverCtx, entry, err)
			continue
		}
		s.markSent(recoverCtx, entry)
		recovered++
	}
	if recovered > 0 {
		s.log.Info("recovered pending notifications", "count", recovered)
	}
}

// retryLoop runs after startup recovery so the two paths never send the same
// pending row concurrently. Each tick only processes rows that are due.
func (s *Service) retryLoop(ctx context.Context) {
	interval := s.retryInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runSafely("retry", func() {
				s.retryDue(ctx)
			})
		}
	}
}

func (s *Service) retryDue(ctx context.Context) {
	retryCtx, cancel := context.WithTimeout(ctx, pendingRecoveryBudget)
	defer cancel()
	limit := s.retryBatchSize
	if limit <= 0 {
		limit = 20
	}
	entries, err := s.store.DuePendingNotifications(retryCtx, time.Now().UTC(), limit)
	if err != nil {
		s.log.Error("load due notifications", "error", err)
		return
	}
	// Preserve creation order per channel/target, but let unrelated targets make
	// progress while one slow channel is waiting on its own timeout.
	groups := make(map[string][]model.NotificationLog)
	order := make([]string, 0, len(entries))
	for _, entry := range entries {
		key := entry.Channel + "\x00" + strconv.FormatInt(entry.ChannelID, 10) + "\x00" + entry.Target
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], entry)
	}

	semaphore := make(chan struct{}, retryConcurrency)
	var wg sync.WaitGroup
	for _, key := range order {
		group := groups[key]
		wg.Add(1)
		semaphore <- struct{}{}
		go func() {
			defer wg.Done()
			defer s.recoverTask("retry-group")
			defer func() { <-semaphore }()
			for _, entry := range group {
				if retryCtx.Err() != nil {
					return
				}
				s.retryEntry(retryCtx, entry)
			}
		}()
	}
	wg.Wait()
}

func (s *Service) retryEntry(ctx context.Context, entry model.NotificationLog) {
	if s.blockedByEarlierState(ctx, entry) {
		s.deferBlocked(ctx, entry)
		return
	}
	if s.expired(entry, time.Now().UTC()) {
		s.failExpired(ctx, entry, "notification retry window expired")
		return
	}
	s.log.Info("retrying pending notification", "channel", entry.Channel, "channel_id", entry.ChannelID, "target", entry.Target, "dedupe_key", entry.DedupeKey, "attempts", entry.Attempts)
	if err := s.attemptEntry(ctx, entry); err != nil {
		s.recordFailure(ctx, entry, err)
		return
	}
	s.markSent(ctx, entry)
}

func (s *Service) expired(entry model.NotificationLog, now time.Time) bool {
	age := now.Sub(entry.CreatedAt)
	if age >= pendingMaxAge {
		return true
	}
	maxAge := s.retryMaxAge
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	return entry.Attempts > 0 && age >= maxAge
}

// blockedByEarlierState keeps DOWN, UP, and DOWN reminders for one monitor,
// channel and target in creation order. A newer state notification must not
// overtake an older one that is still queued or being retried.
func (s *Service) blockedByEarlierState(ctx context.Context, entry model.NotificationLog) bool {
	monitorID, ok := stateMonitorID(entry.DedupeKey)
	if !ok || entry.ID <= 0 {
		return false
	}
	older, err := s.store.HasEarlierPendingStateNotification(ctx, entry.ID, monitorID, entry.ChannelID, entry.Target)
	if err != nil {
		s.log.Warn("check earlier pending notification", "channel", entry.Channel, "target", entry.Target, "dedupe_key", entry.DedupeKey, "error", err)
		return false
	}
	return older
}

func (s *Service) deferBlocked(ctx context.Context, entry model.NotificationLog) {
	next := time.Now().UTC().Add(blockedRetryDefer)
	if err := s.store.DeferNotification(ctx, entry.DedupeKey, entry.Channel, entry.ChannelID, entry.Target, next); err != nil {
		s.log.Warn("defer blocked notification", "channel", entry.Channel, "target", entry.Target, "dedupe_key", entry.DedupeKey, "error", err)
	}
}

func (s *Service) deferToEarlierState(ctx context.Context, entry model.NotificationLog) bool {
	if !s.blockedByEarlierState(ctx, entry) {
		return false
	}
	// The claim lease is meant to protect a send in progress. This row will not
	// be sent now, so make it due immediately after the earlier row clears.
	if err := s.store.DeferNotification(ctx, entry.DedupeKey, entry.Channel, entry.ChannelID, entry.Target, time.Now().UTC()); err != nil {
		s.log.Error("defer notification", "channel", entry.Channel, "target", entry.Target, "dedupe_key", entry.DedupeKey, "error", err)
	}
	s.log.Info("notification deferred behind earlier pending notification", "channel", entry.Channel, "channel_id", entry.ChannelID, "target", entry.Target, "dedupe_key", entry.DedupeKey)
	return true
}

func stateMonitorID(key string) (int64, bool) {
	kind, rest, ok := strings.Cut(key, ":")
	if !ok {
		return 0, false
	}
	switch kind {
	case "down", "up", "remind_down":
	default:
		return 0, false
	}
	idText, _, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (s *Service) attemptEntry(ctx context.Context, entry model.NotificationLog) error {
	if strings.TrimSpace(entry.Payload) == "" {
		return permanentError{err: errors.New("pending notification has no payload")}
	}
	if entry.ChannelID <= 0 {
		return permanentError{err: errors.New("notification channel no longer exists")}
	}
	channel, err := s.store.NotificationChannelByID(ctx, entry.ChannelID)
	if errors.Is(err, store.ErrNotFound) {
		return permanentError{err: errors.New("notification channel no longer exists")}
	}
	if err != nil {
		return err
	}
	if !channel.Enabled {
		return permanentError{err: errors.New("notification channel is disabled")}
	}
	switch channel.Type {
	case model.NotificationChannelTelegram:
		if !containsChatID(channel, entry.Target) {
			return permanentError{err: errors.New("telegram chat id is no longer configured")}
		}
		token, err := s.channelSecret(channel)
		if err != nil {
			return permanentError{err: fmt.Errorf("notification channel secret: %w", err)}
		}
		client, cleanup, err := s.httpClient(ctx, channel.ProxyID)
		if err != nil {
			return err
		}
		defer cleanup()
		if err := s.postTelegram(ctx, client, token, entry.Target, entry.Payload); err != nil {
			return err
		}
	case model.NotificationChannelWebhook:
		if err := s.sendWebhookAttempt(ctx, channel, []byte(entry.Payload)); err != nil {
			return err
		}
	default:
		return permanentError{err: fmt.Errorf("unknown notification channel type %q", channel.Type)}
	}
	return nil
}

func (s *Service) failExpired(ctx context.Context, entry model.NotificationLog, reason string) {
	errorText := truncateError(entry.Error)
	if strings.TrimSpace(errorText) == "" {
		errorText = reason
	}
	if err := s.store.FailNotification(ctx, entry.DedupeKey, entry.Channel, entry.ChannelID, entry.Target, errorText, entry.Attempts, time.Now().UTC()); err != nil {
		s.log.Error("record expired notification", "channel", entry.Channel, "target", entry.Target, "dedupe_key", entry.DedupeKey, "error", err)
	}
	s.log.Warn("notification retry expired", "channel", entry.Channel, "channel_id", entry.ChannelID, "target", entry.Target, "dedupe_key", entry.DedupeKey, "attempts", entry.Attempts, "reason", reason, "error", errorText)
}

func (s *Service) recordFailure(ctx context.Context, entry model.NotificationLog, sendErr error) {
	if ctx.Err() != nil {
		// Keep the row pending so startup recovery can resume it.
		s.log.Warn("notification retry interrupted", "channel", entry.Channel, "channel_id", entry.ChannelID, "target", entry.Target, "dedupe_key", entry.DedupeKey)
		return
	}
	now := time.Now().UTC()
	completed := entry.Attempts + 1
	errorText := truncateError(sendErr.Error())
	maxAttempts := s.retryMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 24
	}
	var permanent permanentError
	if errors.As(sendErr, &permanent) || completed >= maxAttempts {
		if err := s.store.FailNotification(ctx, entry.DedupeKey, entry.Channel, entry.ChannelID, entry.Target, errorText, completed, now); err != nil {
			s.log.Error("record failed notification", "channel", entry.Channel, "target", entry.Target, "dedupe_key", entry.DedupeKey, "error", err)
		}
		s.log.Warn("notification send failed permanently", "channel", entry.Channel, "channel_id", entry.ChannelID, "target", entry.Target, "dedupe_key", entry.DedupeKey, "attempts", completed, "error", sendErr)
		return
	}
	schedule := s.retrySchedule
	if schedule == nil {
		schedule = defaultRetryDelay
	}
	delay := schedule(completed)
	var retryAfter retryAfterError
	if errors.As(sendErr, &retryAfter) && retryAfter.wait > 0 {
		delay = retryAfter.wait
		if delay > maxRetryAfter {
			delay = maxRetryAfter
		}
	}
	maxAge := s.retryMaxAge
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	nextAt := now.Add(delay)
	if !entry.CreatedAt.IsZero() && nextAt.After(entry.CreatedAt.Add(maxAge)) {
		if err := s.store.FailNotification(ctx, entry.DedupeKey, entry.Channel, entry.ChannelID, entry.Target, errorText, completed, now); err != nil {
			s.log.Error("record expired notification", "channel", entry.Channel, "target", entry.Target, "dedupe_key", entry.DedupeKey, "error", err)
		}
		s.log.Warn("notification retry window expired", "channel", entry.Channel, "channel_id", entry.ChannelID, "target", entry.Target, "dedupe_key", entry.DedupeKey, "attempts", completed, "error", sendErr)
		return
	}
	if err := s.store.ScheduleNotificationRetry(ctx, entry.DedupeKey, entry.Channel, entry.ChannelID, entry.Target, errorText, completed, nextAt); err != nil {
		s.log.Error("schedule notification retry", "channel", entry.Channel, "target", entry.Target, "dedupe_key", entry.DedupeKey, "error", err)
		return
	}
	s.log.Warn("notification retry scheduled", "channel", entry.Channel, "channel_id", entry.ChannelID, "target", entry.Target, "dedupe_key", entry.DedupeKey, "attempts", completed, "next_attempt_at", nextAt.UTC().Format(time.RFC3339), "error", sendErr)
}

func (s *Service) markSent(ctx context.Context, entry model.NotificationLog) {
	if err := s.store.CompleteNotification(ctx, entry.DedupeKey, entry.Channel, entry.ChannelID, entry.Target, "sent", "", time.Now().UTC()); err != nil {
		s.log.Error("record notification sent", "channel", entry.Channel, "target", entry.Target, "dedupe_key", entry.DedupeKey, "error", err)
		return
	}
	s.log.Info("notification sent", "channel", entry.Channel, "channel_id", entry.ChannelID, "channel_name", entry.ChannelName, "target", entry.Target, "dedupe_key", entry.DedupeKey, "attempts", entry.Attempts+1)
}

func containsChatID(channel model.NotificationChannel, target string) bool {
	for _, chatID := range channel.ChatIDs() {
		if chatID == strings.TrimSpace(target) {
			return true
		}
	}
	return false
}
