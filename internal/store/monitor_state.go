package store

import (
	"context"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func closeOpenDownEvents(ctx context.Context, exec execer, monitorID int64, at time.Time) error {
	if _, err := exec.ExecContext(ctx, `
		UPDATE events SET ended_at_ms = ?
		WHERE monitor_id = ? AND type = ? AND ended_at_ms IS NULL
	`, at.UTC().UnixMilli(), monitorID, model.EventDown); err != nil {
		return fmt.Errorf("close open down events: %w", err)
	}
	return nil
}

func resetMonitorCounters(ctx context.Context, exec execer, monitorID int64, at time.Time) error {
	now := at.UTC().UnixMilli()
	if _, err := exec.ExecContext(ctx, `
		UPDATE monitor_state
		SET consecutive_failures = 0,
		    consecutive_successes = 0,
		    failure_started_at_ms = 0,
		    status_since_ms = CASE WHEN status IN (?, ?) THEN ? ELSE status_since_ms END,
		    updated_at_ms = ?
		WHERE monitor_id = ?
	`, model.StatusWarning, model.StatusDown, now, now, monitorID); err != nil {
		return fmt.Errorf("reset monitor counters: %w", err)
	}
	return nil
}

func resetActiveEpisode(ctx context.Context, exec execer, monitorID int64, at time.Time) error {
	now := at.UTC().UnixMilli()
	if _, err := exec.ExecContext(ctx, `
		UPDATE monitor_state
		SET status = ?,
		    status_since_ms = ?,
		    last_error = '',
		    consecutive_failures = 0,
		    consecutive_successes = 0,
		    failure_started_at_ms = 0,
		    warning_context = '',
		    updated_at_ms = ?
		WHERE monitor_id = ? AND status IN (?, ?)
	`, model.StatusPending, now, now, monitorID, model.StatusDown, model.StatusWarning); err != nil {
		return fmt.Errorf("reset monitor episode: %w", err)
	}
	return nil
}

func resetMonitorToPending(ctx context.Context, exec execer, monitorID int64, at time.Time) error {
	now := at.UTC().UnixMilli()
	if _, err := exec.ExecContext(ctx, `
		UPDATE monitor_state
		SET status = ?,
		    status_since_ms = ?,
		    last_error = '',
		    consecutive_failures = 0,
		    consecutive_successes = 0,
		    failure_started_at_ms = 0,
		    warning_context = '',
		    updated_at_ms = ?
		WHERE monitor_id = ?
	`, model.StatusPending, now, now, monitorID); err != nil {
		return fmt.Errorf("set monitor pending: %w", err)
	}
	return nil
}
