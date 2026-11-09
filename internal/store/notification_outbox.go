package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

// EnqueueNotificationOutbox persists a notification before any network work.
// The in-memory dispatcher only wakes on this row; the row itself is the
// durable record that survives a restart or a slow notification channel.
func (s *Store) EnqueueNotificationOutbox(ctx context.Context, notification model.Notification, now time.Time) error {
	payload, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("encode notification outbox: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO notification_outbox (
			dedupe_key, monitor_id, payload_json, created_at_ms
		) VALUES (?, ?, ?, ?)
	`, notificationOutboxKey(notification, now), notification.MonitorID, string(payload), now.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("insert notification outbox: %w", err)
	}
	return nil
}

func notificationOutboxKey(notification model.Notification, now time.Time) string {
	if notification.Key != "" {
		return notification.Key
	}
	return fmt.Sprintf("outbox:%d:%d", notification.MonitorID, now.UTC().UnixNano())
}

func (s *Store) DueNotificationOutbox(ctx context.Context, now time.Time, limit int) ([]model.NotificationOutboxEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.reader().QueryContext(ctx, `
		SELECT id, dedupe_key, monitor_id, payload_json, created_at_ms,
		       attempts, next_attempt_at_ms
		FROM notification_outbox
		WHERE next_attempt_at_ms IS NULL OR next_attempt_at_ms <= ?
		ORDER BY created_at_ms ASC, id ASC
		LIMIT ?
	`, now.UTC().UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("list notification outbox: %w", err)
	}
	defer rows.Close()

	entries := make([]model.NotificationOutboxEntry, 0, limit)
	for rows.Next() {
		var (
			entry       model.NotificationOutboxEntry
			createdAtMS int64
			nextAt      sql.NullInt64
		)
		if err := rows.Scan(
			&entry.ID, &entry.DedupeKey, &entry.MonitorID, &entry.Payload,
			&createdAtMS, &entry.Attempts, &nextAt,
		); err != nil {
			return nil, fmt.Errorf("scan notification outbox: %w", err)
		}
		entry.CreatedAt = time.UnixMilli(createdAtMS).UTC()
		if nextAt.Valid {
			entry.NextAttemptAt = time.UnixMilli(nextAt.Int64).UTC()
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification outbox: %w", err)
	}
	return entries, nil
}

func (s *Store) CompleteNotificationOutbox(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM notification_outbox WHERE id = ?", id); err != nil {
		return fmt.Errorf("complete notification outbox: %w", err)
	}
	return nil
}

func (s *Store) RescheduleNotificationOutbox(ctx context.Context, id int64, errorText string, attempts int, nextAt time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notification_outbox
		SET attempts = ?, next_attempt_at_ms = ?, last_error = ?
		WHERE id = ?
	`, attempts, nextAt.UTC().UnixMilli(), errorText, id); err != nil {
		return fmt.Errorf("reschedule notification outbox: %w", err)
	}
	return nil
}

func (s *Store) DeleteNotificationOutboxBefore(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM notification_outbox WHERE created_at_ms < ?
	`, before.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("delete notification outbox: %w", err)
	}
	return nil
}
