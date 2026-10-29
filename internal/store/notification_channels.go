package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

var ErrNotificationChannelNameTaken = errors.New("notification channel name already exists")

const notificationChannelSelect = `
	SELECT id, name, type, language, enabled, telegram_chat_ids, webhook_url, webhook_format,
	       secret_enc, proxy_id, created_at_ms, updated_at_ms
	FROM notification_channels
`

func scanNotificationChannel(row scanner) (model.NotificationChannel, error) {
	var (
		channel     model.NotificationChannel
		enabled     int
		proxyID     sql.NullInt64
		createdAtMS int64
		updatedAtMS int64
	)
	err := row.Scan(
		&channel.ID, &channel.Name, &channel.Type, &channel.Language, &enabled,
		&channel.TelegramChatIDs, &channel.WebhookURL, &channel.WebhookFormat, &channel.SecretEnc,
		&proxyID, &createdAtMS, &updatedAtMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.NotificationChannel{}, ErrNotFound
	}
	if err != nil {
		return model.NotificationChannel{}, fmt.Errorf("scan notification channel: %w", err)
	}
	channel.Enabled = enabled != 0
	if proxyID.Valid {
		id := proxyID.Int64
		channel.ProxyID = &id
	}
	channel.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	channel.UpdatedAt = time.UnixMilli(updatedAtMS).UTC()
	return channel, nil
}

func (s *Store) ListNotificationChannels(ctx context.Context) ([]model.NotificationChannel, error) {
	rows, err := s.db.QueryContext(ctx, notificationChannelSelect+" ORDER BY id ASC")
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()

	var channels []model.NotificationChannel
	for rows.Next() {
		channel, err := scanNotificationChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification channels: %w", err)
	}
	return channels, nil
}

func (s *Store) NotificationChannelByID(ctx context.Context, id int64) (model.NotificationChannel, error) {
	return scanNotificationChannel(s.db.QueryRowContext(ctx, notificationChannelSelect+" WHERE id = ?", id))
}

func (s *Store) EnabledNotificationChannelCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_channels WHERE enabled = 1").Scan(&count); err != nil {
		return 0, fmt.Errorf("count notification channels: %w", err)
	}
	return count, nil
}

func (s *Store) CreateNotificationChannel(ctx context.Context, channel model.NotificationChannel, now time.Time) (int64, error) {
	if err := channel.Validate(); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_channels (
			name, type, language, enabled, telegram_chat_ids, webhook_url, webhook_format, secret_enc,
			proxy_id, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		strings.TrimSpace(channel.Name), channel.Type, channel.ResolvedLanguage(), boolInt(channel.Enabled),
		strings.TrimSpace(channel.TelegramChatIDs), strings.TrimSpace(channel.WebhookURL),
		channel.ResolvedWebhookFormat(), channel.SecretEnc, channel.ProxyID,
		now.UTC().UnixMilli(), now.UTC().UnixMilli(),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return 0, ErrNotificationChannelNameTaken
		}
		return 0, fmt.Errorf("insert notification channel: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read notification channel id: %w", err)
	}
	return id, nil
}

func (s *Store) UpdateNotificationChannel(ctx context.Context, channel model.NotificationChannel, now time.Time) error {
	if err := channel.Validate(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE notification_channels SET
			name = ?, type = ?, language = ?, enabled = ?, telegram_chat_ids = ?, webhook_url = ?,
			webhook_format = ?, secret_enc = ?, proxy_id = ?, updated_at_ms = ?
		WHERE id = ?
	`,
		strings.TrimSpace(channel.Name), channel.Type, channel.ResolvedLanguage(), boolInt(channel.Enabled),
		strings.TrimSpace(channel.TelegramChatIDs), strings.TrimSpace(channel.WebhookURL),
		channel.ResolvedWebhookFormat(), channel.SecretEnc, channel.ProxyID, now.UTC().UnixMilli(), channel.ID,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrNotificationChannelNameTaken
		}
		return fmt.Errorf("update notification channel: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update notification channel rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteNotificationChannel(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM notification_channels WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete notification channel: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete notification channel rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) LinkNotificationChannelToUnlinkedMonitors(ctx context.Context, channelID int64) ([]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin notification channel linking: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT m.id
		FROM monitors m
		WHERE m.notify_enabled = 1
		  AND NOT EXISTS (
			SELECT 1 FROM monitor_notifications mn WHERE mn.monitor_id = m.id
		  )
		ORDER BY m.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list unlinked monitors: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan unlinked monitor: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate unlinked monitors: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close unlinked monitors: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_notifications (monitor_id, channel_id) VALUES (?, ?)
		`, id, channelID); err != nil {
			return nil, fmt.Errorf("link notification channel: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit notification channel linking: %w", err)
	}
	return ids, nil
}

func (s *Store) MonitorIDsForNotificationChannel(ctx context.Context, channelID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id FROM monitor_notifications WHERE channel_id = ? ORDER BY monitor_id ASC
	`, channelID)
	if err != nil {
		return nil, fmt.Errorf("list monitors for notification channel: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan monitor for notification channel: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monitors for notification channel: %w", err)
	}
	return ids, nil
}

func (s *Store) NotificationChannelIDsForMonitor(ctx context.Context, monitorID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mn.channel_id
		FROM monitor_notifications mn
		JOIN notification_channels c ON c.id = mn.channel_id
		WHERE mn.monitor_id = ?
		ORDER BY mn.channel_id ASC
	`, monitorID)
	if err != nil {
		return nil, fmt.Errorf("list monitor notification channels: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan monitor notification channel: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monitor notification channels: %w", err)
	}
	return ids, nil
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
