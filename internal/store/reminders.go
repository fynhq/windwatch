package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func (s *Store) ReminderState(ctx context.Context, monitorID int64, kind string) (model.NotificationReminder, bool, error) {
	var (
		state       model.NotificationReminder
		statusSince int64
		nextAt      int64
		updatedAt   int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT monitor_id, kind, status_since_ms, context_key, sent_count, next_at_ms, updated_at_ms
		FROM notification_reminders WHERE monitor_id = ? AND kind = ?
	`, monitorID, kind).Scan(
		&state.MonitorID, &state.Kind, &statusSince, &state.ContextKey,
		&state.SentCount, &nextAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.NotificationReminder{}, false, nil
	}
	if err != nil {
		return model.NotificationReminder{}, false, fmt.Errorf("get reminder state: %w", err)
	}
	state.StatusSince = time.UnixMilli(statusSince).UTC()
	state.NextAt = time.UnixMilli(nextAt).UTC()
	state.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return state, true, nil
}

func (s *Store) SaveReminderState(ctx context.Context, state model.NotificationReminder, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_reminders (
			monitor_id, kind, status_since_ms, context_key, sent_count, next_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(monitor_id, kind) DO UPDATE SET
			status_since_ms = excluded.status_since_ms,
			context_key = excluded.context_key,
			sent_count = excluded.sent_count,
			next_at_ms = excluded.next_at_ms,
			updated_at_ms = excluded.updated_at_ms
	`,
		state.MonitorID, state.Kind, state.StatusSince.UTC().UnixMilli(), state.ContextKey,
		state.SentCount, state.NextAt.UTC().UnixMilli(), now.UTC().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("save reminder state: %w", err)
	}
	return nil
}

func (s *Store) DeleteReminderState(ctx context.Context, monitorID int64, kind string) error {
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM notification_reminders WHERE monitor_id = ? AND kind = ?", monitorID, kind); err != nil {
		return fmt.Errorf("delete reminder state: %w", err)
	}
	return nil
}

func (s *Store) DeleteReminderStates(ctx context.Context, monitorID int64) error {
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM notification_reminders WHERE monitor_id = ?", monitorID); err != nil {
		return fmt.Errorf("delete reminder states: %w", err)
	}
	return nil
}
