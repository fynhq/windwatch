package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.reader().QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()

	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate settings: %w", err)
	}
	return values, nil
}

func (s *Store) SetSettings(ctx context.Context, values map[string]string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin settings update: %w", err)
	}
	defer tx.Rollback()

	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at_ms)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET
				value = excluded.value,
				updated_at_ms = excluded.updated_at_ms
		`, key, value, now.UTC().UnixMilli()); err != nil {
			return fmt.Errorf("set setting %s: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit settings update: %w", err)
	}
	return nil
}

// notificationClaimLease keeps a freshly claimed row out of the retry worker
// while the dispatching goroutine is still making its immediate attempts.
const notificationClaimLease = 2 * time.Minute

func (s *Store) ClaimNotificationID(ctx context.Context, dedupeKey, channel string, channelID int64, channelName, target, payload, monitorName string, now time.Time) (bool, int64, error) {
	claimedAt := now.UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO notify_log (
			dedupe_key, channel, channel_id, channel_name, monitor_name, target, status, payload,
			created_at_ms, next_attempt_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)
	`, dedupeKey, channel, channelID, channelName, monitorName, target, payload, claimedAt.UnixMilli(), claimedAt.Add(notificationClaimLease).UnixMilli())
	if err != nil {
		return false, 0, fmt.Errorf("claim notification: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, 0, fmt.Errorf("claim notification rows: %w", err)
	}
	if affected == 0 {
		return false, 0, nil
	}
	id, err := result.LastInsertId()
	if err != nil {
		return false, 0, fmt.Errorf("claim notification id: %w", err)
	}
	return true, id, nil
}

func (s *Store) CompleteNotification(ctx context.Context, dedupeKey, channel string, channelID int64, target, status, errorText string, sentAt time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notify_log
		SET status = ?, error = ?, sent_at_ms = ?, next_attempt_at_ms = NULL, attempts = attempts + 1
		WHERE dedupe_key = ? AND channel = ? AND channel_id = ? AND target = ?
	`, status, errorText, sentAt.UTC().UnixMilli(), dedupeKey, channel, channelID, target); err != nil {
		return fmt.Errorf("complete notification: %w", err)
	}
	return nil
}

func (s *Store) FailNotification(ctx context.Context, dedupeKey, channel string, channelID int64, target, errorText string, attempts int, failedAt time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notify_log
		SET status = 'failed', error = ?, attempts = ?, sent_at_ms = ?, next_attempt_at_ms = NULL
		WHERE dedupe_key = ? AND channel = ? AND channel_id = ? AND target = ?
	`, errorText, attempts, failedAt.UTC().UnixMilli(), dedupeKey, channel, channelID, target); err != nil {
		return fmt.Errorf("fail notification: %w", err)
	}
	return nil
}

func (s *Store) ScheduleNotificationRetry(ctx context.Context, dedupeKey, channel string, channelID int64, target, errorText string, attempts int, nextAt time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notify_log
		SET status = 'pending', error = ?, attempts = ?, next_attempt_at_ms = ?, sent_at_ms = NULL
		WHERE dedupe_key = ? AND channel = ? AND channel_id = ? AND target = ?
	`, errorText, attempts, nextAt.UTC().UnixMilli(), dedupeKey, channel, channelID, target); err != nil {
		return fmt.Errorf("schedule notification retry: %w", err)
	}
	return nil
}

func (s *Store) DeferNotification(ctx context.Context, dedupeKey, channel string, channelID int64, target string, nextAt time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notify_log SET next_attempt_at_ms = ?
		WHERE dedupe_key = ? AND channel = ? AND channel_id = ? AND target = ? AND status = 'pending'
	`, nextAt.UTC().UnixMilli(), dedupeKey, channel, channelID, target); err != nil {
		return fmt.Errorf("defer notification: %w", err)
	}
	return nil
}

func (s *Store) DuePendingNotifications(ctx context.Context, now time.Time, limit int) ([]model.NotificationLog, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.reader().QueryContext(ctx, `
		SELECT id, dedupe_key, channel, channel_id, channel_name, monitor_name, target, status, error, payload,
			created_at_ms, sent_at_ms, attempts, next_attempt_at_ms
		FROM notify_log
		WHERE status = 'pending' AND (next_attempt_at_ms IS NULL OR next_attempt_at_ms <= ?)
		ORDER BY created_at_ms ASC, id ASC
		LIMIT ?
	`, now.UTC().UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("list due notifications: %w", err)
	}
	logs, err := scanNotificationLogs(rows)
	if err != nil {
		return nil, fmt.Errorf("list due notifications: %w", err)
	}
	return logs, nil
}

func (s *Store) PendingNotifications(ctx context.Context, limit int) ([]model.NotificationLog, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.reader().QueryContext(ctx, `
		SELECT id, dedupe_key, channel, channel_id, channel_name, monitor_name, target, status, error, payload,
			created_at_ms, sent_at_ms, attempts, next_attempt_at_ms
		FROM notify_log WHERE status = 'pending' ORDER BY created_at_ms ASC, id ASC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending notifications: %w", err)
	}
	logs, err := scanNotificationLogs(rows)
	if err != nil {
		return nil, fmt.Errorf("list pending notifications: %w", err)
	}
	return logs, nil
}

func (s *Store) HasEarlierPendingStateNotification(ctx context.Context, id, monitorID, channelID int64, target string) (bool, error) {
	if id <= 0 || monitorID <= 0 {
		return false, nil
	}
	var count int
	if err := s.reader().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM notify_log
		WHERE status = 'pending' AND channel_id = ? AND target = ? AND id < ?
			AND (dedupe_key LIKE ? OR dedupe_key LIKE ? OR dedupe_key LIKE ?)
	`, channelID, target, id,
		fmt.Sprintf("down:%d:%%", monitorID),
		fmt.Sprintf("up:%d:%%", monitorID),
		fmt.Sprintf("remind_down:%d:%%", monitorID),
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check earlier pending state notification: %w", err)
	}
	return count > 0, nil
}

func (s *Store) DeleteNotificationsBefore(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM notify_log WHERE status <> 'pending' AND created_at_ms < ?
	`, before.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("delete old notifications: %w", err)
	}
	return nil
}

func (s *Store) RecentNotifications(ctx context.Context, limit int) ([]model.NotificationLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.reader().QueryContext(ctx, `
		SELECT id, dedupe_key, channel, channel_id, channel_name, monitor_name, target, status, error, payload,
			created_at_ms, sent_at_ms, attempts, next_attempt_at_ms
		FROM notify_log ORDER BY created_at_ms DESC, id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list notification log: %w", err)
	}
	logs, err := scanNotificationLogs(rows)
	if err != nil {
		return nil, fmt.Errorf("list notification log: %w", err)
	}
	return logs, nil
}

func scanNotificationLogs(rows *sql.Rows) ([]model.NotificationLog, error) {
	defer rows.Close()

	var logs []model.NotificationLog
	for rows.Next() {
		var (
			entry   model.NotificationLog
			created int64
			sentAt  sql.NullInt64
			nextAt  sql.NullInt64
		)
		if err := rows.Scan(
			&entry.ID, &entry.DedupeKey, &entry.Channel, &entry.ChannelID, &entry.ChannelName, &entry.MonitorName,
			&entry.Target, &entry.Status, &entry.Error, &entry.Payload, &created, &sentAt, &entry.Attempts, &nextAt,
		); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		entry.CreatedAt = time.UnixMilli(created).UTC()
		if sentAt.Valid {
			entry.SentAt = time.UnixMilli(sentAt.Int64).UTC()
		}
		if nextAt.Valid {
			entry.NextAttemptAt = time.UnixMilli(nextAt.Int64).UTC()
		}
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}
	return logs, nil
}
