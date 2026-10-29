package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

// PrunePublicStatusMonitorIDs removes whitelist entries that no longer refer to
// an existing monitor. It also repairs duplicate or malformed ID lists.
func (s *Store) PrunePublicStatusMonitorIDs(ctx context.Context, now time.Time) error {
	values, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	raw, ok := values[model.SettingPublicStatusMonitorIDs]
	if !ok {
		return nil
	}

	var ids []int64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		ids = nil
	}

	rows, err := s.db.QueryContext(ctx, "SELECT id FROM monitors")
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
	return s.SetSettings(ctx, map[string]string{
		model.SettingPublicStatusMonitorIDs: string(encoded),
	}, now)
}
