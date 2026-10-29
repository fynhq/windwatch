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

func (s *Store) CreatePushToken(ctx context.Context, monitorID int64, tokenHash, tokenEnc string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO push_tokens (monitor_id, token_hash, token_enc, created_at_ms)
		VALUES (?, ?, ?, ?)
	`, monitorID, tokenHash, tokenEnc, now.UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("insert push token: %w", err)
	}
	return nil
}

func (s *Store) PushTokenByHash(ctx context.Context, tokenHash string) (model.PushToken, error) {
	return scanPushToken(s.db.QueryRowContext(ctx, pushTokenSelect+" WHERE token_hash = ?", tokenHash))
}

func (s *Store) PushTokenByMonitorID(ctx context.Context, monitorID int64) (model.PushToken, error) {
	return scanPushToken(s.db.QueryRowContext(ctx, pushTokenSelect+" WHERE monitor_id = ?", monitorID))
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
