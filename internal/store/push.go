package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

const pushTokenSelect = `
	SELECT monitor_id, token_hash, token_enc, last_heartbeat_ms, created_at_ms
	FROM push_tokens
`

func scanPushToken(row scanner) (model.PushToken, error) {
	var (
		token         model.PushToken
		lastHeartbeat sql.NullInt64
		createdAtMS   int64
	)
	err := row.Scan(&token.MonitorID, &token.TokenHash, &token.TokenEnc, &lastHeartbeat, &createdAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PushToken{}, ErrNotFound
	}
	if err != nil {
		return model.PushToken{}, fmt.Errorf("scan push token: %w", err)
	}
	if lastHeartbeat.Valid {
		token.LastHeartbeat = time.UnixMilli(lastHeartbeat.Int64).UTC()
	}
	token.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	return token, nil
}

func (s *Store) PushTokenByHash(ctx context.Context, tokenHash string) (model.PushToken, error) {
	return scanPushToken(s.reader().QueryRowContext(ctx, pushTokenSelect+" WHERE token_hash = ?", tokenHash))
}

func (s *Store) PushTokenByMonitorID(ctx context.Context, monitorID int64) (model.PushToken, error) {
	return scanPushToken(s.reader().QueryRowContext(ctx, pushTokenSelect+" WHERE monitor_id = ?", monitorID))
}

func (s *Store) ReplacePushToken(ctx context.Context, monitorID int64, tokenHash, tokenEnc string, now time.Time) error {
	if tokenHash == "" || tokenEnc == "" {
		return errors.New("push token must not be empty")
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace push token: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO push_tokens (monitor_id, token_hash, token_enc, created_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(monitor_id) DO UPDATE SET
			token_hash = excluded.token_hash,
			token_enc = excluded.token_enc,
			created_at_ms = excluded.created_at_ms,
			last_heartbeat_ms = NULL
	`, monitorID, tokenHash, tokenEnc, now.UnixMilli()); err != nil {
		return fmt.Errorf("replace push token: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitor_state
		SET status = ?,
		    status_since_ms = ?,
		    last_error = '',
		    consecutive_failures = 0,
		    consecutive_successes = 0,
		    warning_context = '',
		    updated_at_ms = ?
		WHERE monitor_id = ?
	`, model.StatusPending, now.UnixMilli(), now.UnixMilli(), monitorID); err != nil {
		return fmt.Errorf("reset push monitor state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE events SET ended_at_ms = ?
		WHERE monitor_id = ? AND type = ? AND ended_at_ms IS NULL
	`, now.UnixMilli(), monitorID, model.EventDown); err != nil {
		return fmt.Errorf("close push monitor events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace push token: %w", err)
	}
	return nil
}

func (s *Store) RecordHeartbeat(ctx context.Context, monitorID int64, now time.Time) error {
	result, err := s.db.ExecContext(ctx, "UPDATE push_tokens SET last_heartbeat_ms = ? WHERE monitor_id = ?", now.UTC().UnixMilli(), monitorID)
	if err != nil {
		return fmt.Errorf("record heartbeat: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("record heartbeat rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
