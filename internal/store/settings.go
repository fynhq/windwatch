package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM settings")
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

func (s *Store) ClaimNotification(ctx context.Context, dedupeKey, channel string, channelID int64, channelName, target, payload string, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO notify_log (
			dedupe_key, channel, channel_id, channel_name, target, status, payload, created_at_ms
		) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)
	`, dedupeKey, channel, channelID, channelName, target, payload, now.UTC().UnixMilli())
	if err != nil {
		return false, fmt.Errorf("claim notification: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim notification rows: %w", err)
	}
	return affected > 0, nil
}

func (s *Store) CompleteNotification(ctx context.Context, dedupeKey, channel string, channelID int64, target, status, errorText string, sentAt time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notify_log
		SET status = ?, error = ?, sent_at_ms = ?
		WHERE dedupe_key = ? AND channel = ? AND channel_id = ? AND target = ?
	`, status, errorText, sentAt.UTC().UnixMilli(), dedupeKey, channel, channelID, target); err != nil {
		return fmt.Errorf("complete notification: %w", err)
	}
	return nil
}

func (s *Store) PendingNotifications(ctx context.Context, limit int) ([]model.NotificationLog, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT dedupe_key, channel, channel_id, channel_name, target, status, error, payload, created_at_ms, sent_at_ms
		FROM notify_log WHERE status = 'pending' ORDER BY created_at_ms ASC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending notifications: %w", err)
	}
	defer rows.Close()

	var logs []model.NotificationLog
	for rows.Next() {
		var (
			entry   model.NotificationLog
			created int64
			sentAt  sql.NullInt64
		)
		if err := rows.Scan(
			&entry.DedupeKey, &entry.Channel, &entry.ChannelID, &entry.ChannelName,
			&entry.Target, &entry.Status, &entry.Error, &entry.Payload, &created, &sentAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending notification: %w", err)
		}
		entry.CreatedAt = time.UnixMilli(created).UTC()
		if sentAt.Valid {
			entry.SentAt = time.UnixMilli(sentAt.Int64).UTC()
		}
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending notifications: %w", err)
	}
	return logs, nil
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT dedupe_key, channel, channel_id, channel_name, target, status, error, payload, created_at_ms, sent_at_ms
		FROM notify_log ORDER BY created_at_ms DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list notification log: %w", err)
	}
	defer rows.Close()

	var logs []model.NotificationLog
	for rows.Next() {
		var (
			entry   model.NotificationLog
			created int64
			sentAt  sql.NullInt64
		)
		if err := rows.Scan(
			&entry.DedupeKey, &entry.Channel, &entry.ChannelID, &entry.ChannelName,
			&entry.Target, &entry.Status, &entry.Error, &entry.Payload, &created, &sentAt,
		); err != nil {
			return nil, fmt.Errorf("scan notification log: %w", err)
		}
		entry.CreatedAt = time.UnixMilli(created).UTC()
		if sentAt.Valid {
			entry.SentAt = time.UnixMilli(sentAt.Int64).UTC()
		}
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification log: %w", err)
	}
	return logs, nil
}
