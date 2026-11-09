package notify

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

const (
	outboxBatchSize    = 20
	outboxPollInterval = time.Second
	outboxMaxAge       = 24 * time.Hour
	outboxMaxAttempts  = 24
)

// outboxLoop is the durable half of notification delivery. Enqueue writes the
// notification to notification_outbox before waking this loop; a crash or a
// slow channel therefore cannot lose an event before it reaches notify_log.
func (s *Service) outboxLoop(ctx context.Context) {
	lastCleanup := time.Time{}
	s.processOutbox(ctx)
	prune := func() {
		if !lastCleanup.IsZero() && time.Since(lastCleanup) < time.Hour {
			return
		}
		lastCleanup = time.Now()
		if err := s.store.DeleteNotificationOutboxBefore(ctx, time.Now().UTC().Add(-outboxMaxAge)); err != nil {
			s.log.Warn("prune notification outbox", "error", err)
		}
	}
	prune()

	ticker := time.NewTicker(outboxPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		s.processOutbox(ctx)
		prune()
	}
}

func (s *Service) processOutbox(ctx context.Context) {
	defer s.recoverTask("outbox")
	for {
		if ctx.Err() != nil {
			return
		}
		entries, err := s.store.DueNotificationOutbox(ctx, time.Now().UTC(), outboxBatchSize)
		if err != nil {
			s.log.Error("load notification outbox", "error", err)
			return
		}
		if len(entries) == 0 {
			return
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return
			}
			var notification model.Notification
			if err := json.Unmarshal([]byte(entry.Payload), &notification); err != nil {
				s.log.Error("decode notification outbox", "outbox_id", entry.ID, "dedupe_key", entry.DedupeKey, "error", err)
				s.deleteOutbox(ctx, entry.ID)
				continue
			}
			if err := s.dispatch(ctx, notification); err != nil {
				if ctx.Err() != nil {
					return
				}
				attempts := entry.Attempts + 1
				age := time.Since(entry.CreatedAt)
				if attempts >= outboxMaxAttempts || age >= outboxMaxAge {
					s.log.Error("notification outbox entry expired", "outbox_id", entry.ID, "dedupe_key", entry.DedupeKey, "attempts", attempts, "age", age.String(), "error", err)
					s.deleteOutbox(ctx, entry.ID)
					continue
				}
				nextAt := time.Now().UTC().Add(defaultRetryDelay(attempts))
				if err := s.store.RescheduleNotificationOutbox(ctx, entry.ID, truncateError(err.Error()), attempts, nextAt); err != nil {
					s.log.Error("reschedule notification outbox", "outbox_id", entry.ID, "error", err)
					return
				}
				s.log.Warn("notification outbox retry scheduled", "outbox_id", entry.ID, "dedupe_key", entry.DedupeKey, "attempts", attempts, "next_attempt_at", nextAt.Format(time.RFC3339), "error", err)
				continue
			}
			s.deleteOutbox(ctx, entry.ID)
		}
		if len(entries) < outboxBatchSize {
			return
		}
	}
}

func (s *Service) deleteOutbox(ctx context.Context, id int64) {
	if err := s.store.CompleteNotificationOutbox(ctx, id); err != nil {
		s.log.Error("complete notification outbox", "outbox_id", id, "error", err)
	}
}
