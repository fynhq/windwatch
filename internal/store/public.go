package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func (s *Store) PrunePublicStatusMonitorIDs(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin public status prune: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var raw string
	err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", model.SettingPublicStatusMonitorIDs).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read public monitor ids: %w", err)
	}

	var ids []int64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		ids = nil
	}

	rows, err := tx.QueryContext(ctx, "SELECT id FROM monitors")
	if err != nil {
		return fmt.Errorf("list monitor ids: %w", err)
	}
	existing := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan monitor id: %w", err)
		}
		existing[id] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close monitor ids: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate monitor ids: %w", err)
	}

	seen := make(map[int64]struct{}, len(ids))
	filtered := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if _, ok := existing[id]; !ok {
			continue
		}
		seen[id] = struct{}{}
		filtered = append(filtered, id)
	}

	encoded, err := json.Marshal(filtered)
	if err != nil {
		return fmt.Errorf("encode public monitor ids: %w", err)
	}
	if string(encoded) == raw {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at_ms = excluded.updated_at_ms
	`, model.SettingPublicStatusMonitorIDs, string(encoded), now.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("write public monitor ids: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit public status prune: %w", err)
	}
	return nil
}
