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

const monitorSelect = `
	SELECT
		m.id, m.name, m.type, m.config_json,
		m.interval_seconds, m.timeout_seconds,
		m.fail_threshold, m.recover_threshold,
		m.warning_reminder_count, m.warning_reminder_interval_minutes,
		m.failure_reminder_count, m.failure_reminder_interval_minutes,
		m.notify_enabled, m.enabled, m.proxy_id,
		m.created_at_ms, m.updated_at_ms,
		ms.status, ms.status_since_ms, ms.last_check_ms, ms.last_error,
		ms.consecutive_failures, ms.consecutive_successes, ms.updated_at_ms
	FROM monitors m
	LEFT JOIN monitor_state ms ON ms.monitor_id = m.id
`

type scanner interface {
	Scan(dest ...any) error
}

func scanMonitor(row scanner) (model.Monitor, error) {
	var (
		monitor              model.Monitor
		configJSON           string
		intervalSeconds      int64
		timeoutSeconds       int64
		notifyEnabled        int
		enabled              int
		proxyID              sql.NullInt64
		createdAtMS          int64
		updatedAtMS          int64
		warningCount         int
		warningInterval      int64
		failureCount         int
		failureInterval      int64
		stateStatus          sql.NullString
		statusSinceMS        sql.NullInt64
		lastCheckMS          sql.NullInt64
		lastError            sql.NullString
		consecutiveFailures  sql.NullInt64
		consecutiveSuccesses sql.NullInt64
		stateUpdatedAtMS     sql.NullInt64
	)
	err := row.Scan(
		&monitor.ID, &monitor.Name, &monitor.Type, &configJSON,
		&intervalSeconds, &timeoutSeconds,
		&monitor.FailThreshold, &monitor.RecoverThreshold,
		&warningCount, &warningInterval, &failureCount, &failureInterval,
		&notifyEnabled, &enabled, &proxyID,
		&createdAtMS, &updatedAtMS,
		&stateStatus, &statusSinceMS, &lastCheckMS, &lastError,
		&consecutiveFailures, &consecutiveSuccesses, &stateUpdatedAtMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Monitor{}, ErrNotFound
	}
	if err != nil {
		return model.Monitor{}, fmt.Errorf("scan monitor: %w", err)
	}

	monitor.Interval = time.Duration(intervalSeconds) * time.Second
	monitor.Timeout = time.Duration(timeoutSeconds) * time.Second
	monitor.Reminders = model.NotificationPolicy{
		WarningReminderCount:    warningCount,
		WarningReminderInterval: time.Duration(warningInterval) * time.Minute,
		FailureReminderCount:    failureCount,
		FailureReminderInterval: time.Duration(failureInterval) * time.Minute,
	}.Normalized()
	monitor.NotifyEnabled = notifyEnabled != 0
	monitor.Enabled = enabled != 0
	if proxyID.Valid {
		id := proxyID.Int64
		monitor.ProxyID = &id
	}
	monitor.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	monitor.UpdatedAt = time.UnixMilli(updatedAtMS).UTC()
	if err := monitor.DecodeConfig([]byte(configJSON)); err != nil {
		return model.Monitor{}, err
	}

	monitor.State.Status = model.StatusPending
	monitor.State.StatusSince = monitor.CreatedAt
	monitor.State.UpdatedAt = monitor.CreatedAt
	if stateStatus.Valid {
		monitor.State.Status = model.MonitorStatus(stateStatus.String)
		if statusSinceMS.Valid {
			monitor.State.StatusSince = time.UnixMilli(statusSinceMS.Int64).UTC()
		}
		if lastCheckMS.Valid {
			monitor.State.LastCheck = time.UnixMilli(lastCheckMS.Int64).UTC()
		}
		monitor.State.LastError = lastError.String
		if consecutiveFailures.Valid {
			monitor.State.ConsecutiveFailures = int(consecutiveFailures.Int64)
		}
		if consecutiveSuccesses.Valid {
			monitor.State.ConsecutiveSuccesses = int(consecutiveSuccesses.Int64)
		}
		if stateUpdatedAtMS.Valid {
			monitor.State.UpdatedAt = time.UnixMilli(stateUpdatedAtMS.Int64).UTC()
		}
	}
	return monitor, nil
}

func (s *Store) ListMonitors(ctx context.Context) ([]model.Monitor, error) {
	rows, err := s.db.QueryContext(ctx, monitorSelect+" ORDER BY m.id ASC")
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}

	var monitors []model.Monitor
	for rows.Next() {
		monitor, err := scanMonitor(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		monitors = append(monitors, monitor)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate monitors: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close monitors: %w", err)
	}
	if err := s.attachNotificationChannels(ctx, monitors); err != nil {
		return nil, err
	}
	return monitors, nil
}

func (s *Store) MonitorByID(ctx context.Context, id int64) (model.Monitor, error) {
	row := s.db.QueryRowContext(ctx, monitorSelect+" WHERE m.id = ?", id)
	monitor, err := scanMonitor(row)
	if err != nil {
		return model.Monitor{}, err
	}
	monitors := []model.Monitor{monitor}
	if err := s.attachNotificationChannels(ctx, monitors); err != nil {
		return model.Monitor{}, err
	}
	return monitors[0], nil
}

func (s *Store) MonitorsByIDs(ctx context.Context, ids []int64) ([]model.Monitor, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, monitorSelect+" WHERE m.id IN ("+idPlaceholders(len(ids))+") ORDER BY m.id ASC", args...)
	if err != nil {
		return nil, fmt.Errorf("list monitors by ids: %w", err)
	}

	monitors := make([]model.Monitor, 0, len(ids))
	for rows.Next() {
		monitor, err := scanMonitor(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		monitors = append(monitors, monitor)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate monitors by ids: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close monitors by ids: %w", err)
	}
	if err := s.attachNotificationChannels(ctx, monitors); err != nil {
		return nil, err
	}
	return monitors, nil
}

func (s *Store) attachNotificationChannels(ctx context.Context, monitors []model.Monitor) error {
	if len(monitors) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(monitors))
	byID := make(map[int64]int, len(monitors))
	for index := range monitors {
		ids = append(ids, monitors[index].ID)
		byID[monitors[index].ID] = index
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT mn.monitor_id, mn.channel_id, c.enabled
		FROM monitor_notifications mn
		JOIN notification_channels c ON c.id = mn.channel_id
		WHERE mn.monitor_id IN (`+idPlaceholders(len(ids))+`)
		ORDER BY mn.monitor_id ASC, mn.channel_id ASC
	`, int64Args(ids)...)
	if err != nil {
		return fmt.Errorf("list monitor notification channels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			monitorID, channelID int64
			enabled              int
		)
		if err := rows.Scan(&monitorID, &channelID, &enabled); err != nil {
			return fmt.Errorf("scan monitor notification channel: %w", err)
		}
		if index, ok := byID[monitorID]; ok {
			monitors[index].NotificationChannelIDs = append(monitors[index].NotificationChannelIDs, channelID)
			if enabled != 0 {
				monitors[index].EnabledNotificationChannelIDs = append(monitors[index].EnabledNotificationChannelIDs, channelID)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate monitor notification channels: %w", err)
	}
	return nil
}

func int64Args(ids []int64) []any {
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	return args
}

func (s *Store) CreateMonitor(ctx context.Context, monitor model.Monitor, now time.Time) (int64, error) {
	if err := monitor.Validate(); err != nil {
		return 0, err
	}
	configJSON, err := monitor.ConfigJSON()
	if err != nil {
		return 0, err
	}
	reminders := monitor.Reminders.Normalized()

	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin monitor creation: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO monitors (
			name, type, config_json, interval_seconds, timeout_seconds,
			fail_threshold, recover_threshold,
			warning_reminder_count, warning_reminder_interval_minutes,
			failure_reminder_count, failure_reminder_interval_minutes,
			notify_enabled, enabled, proxy_id,
			created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		monitor.Name,
		monitor.Type,
		string(configJSON),
		int64(monitor.Interval/time.Second),
		int64(monitor.Timeout/time.Second),
		monitor.FailThreshold,
		monitor.RecoverThreshold,
		reminders.WarningReminderCount,
		int64(reminders.WarningReminderInterval/time.Minute),
		reminders.FailureReminderCount,
		int64(reminders.FailureReminderInterval/time.Minute),
		boolInt(monitor.NotifyEnabled),
		boolInt(monitor.Enabled),
		monitor.ProxyID,
		now.UnixMilli(),
		now.UnixMilli(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert monitor: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read monitor id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_state (
			monitor_id, status, status_since_ms, last_error,
			consecutive_failures, consecutive_successes, updated_at_ms
		) VALUES (?, ?, ?, '', 0, 0, ?)
	`, id, model.StatusPending, now.UnixMilli(), now.UnixMilli()); err != nil {
		return 0, fmt.Errorf("insert monitor state: %w", err)
	}
	if err := insertMonitorNotificationChannels(ctx, tx, id, monitor.NotificationChannelIDs); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit monitor creation: %w", err)
	}
	return id, nil
}

func (s *Store) UpdateMonitor(ctx context.Context, monitor model.Monitor, now time.Time) error {
	if err := monitor.Validate(); err != nil {
		return err
	}
	configJSON, err := monitor.ConfigJSON()
	if err != nil {
		return err
	}
	reminders := monitor.Reminders.Normalized()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin monitor update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE monitors SET
			name = ?, config_json = ?, interval_seconds = ?, timeout_seconds = ?,
			fail_threshold = ?, recover_threshold = ?,
			warning_reminder_count = ?, warning_reminder_interval_minutes = ?,
			failure_reminder_count = ?, failure_reminder_interval_minutes = ?,
			notify_enabled = ?, proxy_id = ?, updated_at_ms = ?
		WHERE id = ?
	`,
		monitor.Name,
		string(configJSON),
		int64(monitor.Interval/time.Second),
		int64(monitor.Timeout/time.Second),
		monitor.FailThreshold,
		monitor.RecoverThreshold,
		reminders.WarningReminderCount,
		int64(reminders.WarningReminderInterval/time.Minute),
		reminders.FailureReminderCount,
		int64(reminders.FailureReminderInterval/time.Minute),
		boolInt(monitor.NotifyEnabled),
		monitor.ProxyID,
		now.UTC().UnixMilli(),
		monitor.ID,
	)
	if err != nil {
		return fmt.Errorf("update monitor: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update monitor rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM monitor_notifications WHERE monitor_id = ?", monitor.ID); err != nil {
		return fmt.Errorf("clear monitor notification channels: %w", err)
	}
	if err := insertMonitorNotificationChannels(ctx, tx, monitor.ID, monitor.NotificationChannelIDs); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit monitor update: %w", err)
	}
	return nil
}

func insertMonitorNotificationChannels(ctx context.Context, tx *sql.Tx, monitorID int64, channelIDs []int64) error {
	seen := make(map[int64]struct{}, len(channelIDs))
	for _, channelID := range channelIDs {
		if channelID <= 0 {
			continue
		}
		if _, ok := seen[channelID]; ok {
			continue
		}
		seen[channelID] = struct{}{}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_notifications (monitor_id, channel_id) VALUES (?, ?)
		`, monitorID, channelID); err != nil {
			return fmt.Errorf("link monitor notification channel: %w", err)
		}
	}
	return nil
}

func (s *Store) SetMonitorEnabled(ctx context.Context, id int64, enabled bool) error {
	result, err := s.db.ExecContext(ctx, "UPDATE monitors SET enabled = ? WHERE id = ?", boolInt(enabled), id)
	if err != nil {
		return fmt.Errorf("set monitor enabled: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("set monitor enabled rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteMonitor(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM monitors WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete monitor: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete monitor rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RecordCheck(ctx context.Context, check model.Check, state model.MonitorState, event *model.Event, closeDownEvent bool) error {
	metaJSON := check.MetaJSON
	if strings.TrimSpace(metaJSON) == "" {
		metaJSON = "{}"
	}
	eventMeta := "{}"
	if event != nil && strings.TrimSpace(event.MetaJSON) != "" {
		eventMeta = event.MetaJSON
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin check record: %w", err)
	}
	defer tx.Rollback()

	var httpStatus any
	if check.HTTPStatus != 0 {
		httpStatus = check.HTTPStatus
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO checks (
			monitor_id, checked_at_ms, status, latency_ms, http_status,
			error_kind, error_message, meta_json, cert_fingerprint
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		check.MonitorID,
		check.CheckedAt.UTC().UnixMilli(),
		check.Status,
		check.Latency.Milliseconds(),
		httpStatus,
		check.ErrorKind,
		check.ErrorMessage,
		metaJSON,
		check.CertFingerprint,
	); err != nil {
		return fmt.Errorf("insert check: %w", err)
	}

	var lastCheck any
	if !state.LastCheck.IsZero() {
		lastCheck = state.LastCheck.UTC().UnixMilli()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_state (
			monitor_id, status, status_since_ms, last_check_ms, last_error,
			consecutive_failures, consecutive_successes, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(monitor_id) DO UPDATE SET
			status = excluded.status,
			status_since_ms = excluded.status_since_ms,
			last_check_ms = excluded.last_check_ms,
			last_error = excluded.last_error,
			consecutive_failures = excluded.consecutive_failures,
			consecutive_successes = excluded.consecutive_successes,
			updated_at_ms = excluded.updated_at_ms
	`,
		check.MonitorID,
		state.Status,
		state.StatusSince.UTC().UnixMilli(),
		lastCheck,
		state.LastError,
		state.ConsecutiveFailures,
		state.ConsecutiveSuccesses,
		state.UpdatedAt.UTC().UnixMilli(),
	); err != nil {
		return fmt.Errorf("upsert monitor state: %w", err)
	}

	if closeDownEvent {
		if _, err := tx.ExecContext(ctx, `
			UPDATE events SET ended_at_ms = ?
			WHERE monitor_id = ? AND type = ? AND ended_at_ms IS NULL
		`, check.CheckedAt.UTC().UnixMilli(), check.MonitorID, model.EventDown); err != nil {
			return fmt.Errorf("close down event: %w", err)
		}
	}
	if event != nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events (monitor_id, type, message, started_at_ms, meta_json)
			VALUES (?, ?, ?, ?, ?)
		`,
			check.MonitorID,
			event.Type,
			event.Message,
			event.StartedAt.UTC().UnixMilli(),
			eventMeta,
		); err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit check record: %w", err)
	}
	return nil
}

// RecentChecksByMonitor runs one indexed top-N query per monitor. A window
// function would scan all retained checks on every dashboard refresh.
func (s *Store) RecentChecksByMonitor(ctx context.Context, monitorIDs []int64, limit int) (map[int64][]model.Check, error) {
	result := make(map[int64][]model.Check, len(monitorIDs))
	for _, monitorID := range monitorIDs {
		checks, err := s.RecentChecks(ctx, monitorID, limit)
		if err != nil {
			return nil, err
		}
		result[monitorID] = checks
	}
	return result, nil
}

func (s *Store) RecentChecks(ctx context.Context, monitorID int64, limit int) ([]model.Check, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id, checked_at_ms, status, latency_ms, http_status,
		       error_kind, error_message, meta_json, cert_fingerprint
		FROM checks
		WHERE monitor_id = ?
		ORDER BY checked_at_ms DESC
		LIMIT ?
	`, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent checks: %w", err)
	}
	defer rows.Close()

	var checks []model.Check
	for rows.Next() {
		var (
			check      model.Check
			checkedAt  int64
			httpStatus sql.NullInt64
			latencyMS  int64
			certFP     sql.NullString
		)
		if err := rows.Scan(
			&check.MonitorID, &checkedAt, &check.Status, &latencyMS, &httpStatus,
			&check.ErrorKind, &check.ErrorMessage, &check.MetaJSON,
			&certFP,
		); err != nil {
			return nil, fmt.Errorf("scan recent check: %w", err)
		}
		check.CheckedAt = time.UnixMilli(checkedAt).UTC()
		check.Latency = time.Duration(latencyMS) * time.Millisecond
		if httpStatus.Valid {
			check.HTTPStatus = int(httpStatus.Int64)
		}
		check.CertFingerprint = certFP.String
		checks = append(checks, check)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent checks: %w", err)
	}
	return checks, nil
}

func (s *Store) CloseOpenDownEvents(ctx context.Context, monitorID int64, at time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE events SET ended_at_ms = ?
		WHERE monitor_id = ? AND type = ? AND ended_at_ms IS NULL
	`, at.UTC().UnixMilli(), monitorID, model.EventDown); err != nil {
		return fmt.Errorf("close open down events: %w", err)
	}
	return nil
}

func (s *Store) ResetDownStatusSince(ctx context.Context, monitorID int64, at time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE monitor_state SET status_since_ms = ?, updated_at_ms = ?
		WHERE monitor_id = ? AND status = ?
	`, at.UTC().UnixMilli(), at.UTC().UnixMilli(), monitorID, model.StatusDown); err != nil {
		return fmt.Errorf("reset down status since: %w", err)
	}
	return nil
}

func (s *Store) EnsureOpenDownEvent(ctx context.Context, monitorID int64, at time.Time, message string) error {
	var open int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE monitor_id = ? AND type = ? AND ended_at_ms IS NULL
	`, monitorID, model.EventDown).Scan(&open); err != nil {
		return fmt.Errorf("count open down events: %w", err)
	}
	if open > 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO events (monitor_id, type, message, started_at_ms, meta_json)
		VALUES (?, ?, ?, ?, '{}')
	`, monitorID, model.EventDown, message, at.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("open down event: %w", err)
	}
	return nil
}

// OpenDownEventStart returns the start of the open DOWN event. The scheduler
// uses it to measure an outage from the first failed check rather than from
// the later threshold confirmation.
func (s *Store) OpenDownEventStart(ctx context.Context, monitorID int64) (time.Time, bool, error) {
	var startedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT started_at_ms FROM events
		WHERE monitor_id = ? AND type = ? AND ended_at_ms IS NULL
		ORDER BY started_at_ms DESC
		LIMIT 1
	`, monitorID, model.EventDown).Scan(&startedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("load open down event: %w", err)
	}
	return time.UnixMilli(startedAt).UTC(), true, nil
}

func (s *Store) RecentEvents(ctx context.Context, monitorID int64, limit int) ([]model.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT e.id, e.monitor_id, m.name, e.type, e.message, e.started_at_ms, e.ended_at_ms, e.meta_json
		FROM events e
		JOIN monitors m ON m.id = e.monitor_id
	`
	var args []any
	if monitorID > 0 {
		query += " WHERE e.monitor_id = ?"
		args = append(args, monitorID)
	}
	query += " ORDER BY e.started_at_ms DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []model.Event
	for rows.Next() {
		var (
			event     model.Event
			startedAt int64
			endedAt   sql.NullInt64
		)
		if err := rows.Scan(
			&event.ID, &event.MonitorID, &event.MonitorName, &event.Type,
			&event.Message, &startedAt, &endedAt, &event.MetaJSON,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		event.StartedAt = time.UnixMilli(startedAt).UTC()
		if endedAt.Valid {
			t := time.UnixMilli(endedAt.Int64).UTC()
			event.EndedAt = &t
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

func (s *Store) UptimeStats(ctx context.Context, monitorID int64, since time.Time) (model.UptimeStats, error) {
	var (
		stats      model.UptimeStats
		avgLatency float64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'up' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'warning' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'down' THEN 1 ELSE 0 END), 0),
		       COALESCE(AVG(latency_ms), 0)
		FROM checks
		WHERE monitor_id = ? AND checked_at_ms >= ?
	`, monitorID, since.UTC().UnixMilli()).Scan(
		&stats.Total, &stats.Up, &stats.Warning, &stats.Down, &avgLatency,
	)
	if err != nil {
		return model.UptimeStats{}, fmt.Errorf("get uptime stats: %w", err)
	}
	stats.AvgLatency = time.Duration(avgLatency) * time.Millisecond
	return stats, nil
}

func (s *Store) UptimeStatsForMonitors(ctx context.Context, monitorIDs []int64, since time.Time) (map[int64]model.UptimeStats, error) {
	result := make(map[int64]model.UptimeStats, len(monitorIDs))
	if len(monitorIDs) == 0 {
		return result, nil
	}
	args := make([]any, 0, len(monitorIDs)+1)
	for _, id := range monitorIDs {
		args = append(args, id)
	}
	args = append(args, since.UTC().UnixMilli())
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id, COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'up' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'warning' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'down' THEN 1 ELSE 0 END), 0),
		       COALESCE(AVG(latency_ms), 0)
		FROM checks
		WHERE monitor_id IN (`+idPlaceholders(len(monitorIDs))+`) AND checked_at_ms >= ?
		GROUP BY monitor_id
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("get uptime stats for monitors: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			monitorID  int64
			stats      model.UptimeStats
			avgLatency float64
		)
		if err := rows.Scan(&monitorID, &stats.Total, &stats.Up, &stats.Warning, &stats.Down, &avgLatency); err != nil {
			return nil, fmt.Errorf("scan uptime stats: %w", err)
		}
		stats.AvgLatency = time.Duration(avgLatency) * time.Millisecond
		result[monitorID] = stats
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate uptime stats: %w", err)
	}
	return result, nil
}

func (s *Store) HourlyBuckets(ctx context.Context, since time.Time) (map[int64][]model.HourBucket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id,
		       (checked_at_ms / 3600000) * 3600000 AS bucket_start_ms,
		       COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'up' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'warning' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'down' THEN 1 ELSE 0 END), 0),
		       COALESCE(AVG(latency_ms), 0)
		FROM checks
		WHERE checked_at_ms >= ?
		GROUP BY monitor_id, bucket_start_ms
		ORDER BY bucket_start_ms ASC
	`, since.UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("get hourly buckets: %w", err)
	}
	defer rows.Close()

	buckets := make(map[int64][]model.HourBucket)
	for rows.Next() {
		var (
			bucket     model.HourBucket
			bucketMS   int64
			avgLatency float64
		)
		if err := rows.Scan(
			&bucket.MonitorID, &bucketMS, &bucket.Total, &bucket.Up,
			&bucket.Warning, &bucket.Down, &avgLatency,
		); err != nil {
			return nil, fmt.Errorf("scan hourly bucket: %w", err)
		}
		bucket.Start = time.UnixMilli(bucketMS).UTC()
		bucket.AvgLatency = time.Duration(avgLatency) * time.Millisecond
		buckets[bucket.MonitorID] = append(buckets[bucket.MonitorID], bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hourly buckets: %w", err)
	}
	return buckets, nil
}

func (s *Store) HourlyBucketsForMonitor(ctx context.Context, monitorID int64, since time.Time) ([]model.HourBucket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT (checked_at_ms / 3600000) * 3600000 AS bucket_start_ms,
		       COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'up' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'warning' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'down' THEN 1 ELSE 0 END), 0),
		       COALESCE(AVG(latency_ms), 0)
		FROM checks
		WHERE monitor_id = ? AND checked_at_ms >= ?
		GROUP BY bucket_start_ms
		ORDER BY bucket_start_ms ASC
	`, monitorID, since.UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("get hourly buckets for monitor: %w", err)
	}
	defer rows.Close()

	var buckets []model.HourBucket
	for rows.Next() {
		var (
			bucket     model.HourBucket
			bucketMS   int64
			avgLatency float64
		)
		if err := rows.Scan(&bucketMS, &bucket.Total, &bucket.Up, &bucket.Warning, &bucket.Down, &avgLatency); err != nil {
			return nil, fmt.Errorf("scan hourly bucket: %w", err)
		}
		bucket.MonitorID = monitorID
		bucket.Start = time.UnixMilli(bucketMS).UTC()
		bucket.AvgLatency = time.Duration(avgLatency) * time.Millisecond
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hourly buckets: %w", err)
	}
	return buckets, nil
}

func idPlaceholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func (s *Store) DeleteChecksBefore(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM checks WHERE checked_at_ms < ?", before.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("delete old checks: %w", err)
	}
	return nil
}

func (s *Store) DeleteEventsBefore(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM events WHERE ended_at_ms IS NOT NULL AND ended_at_ms < ?", before.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("delete old events: %w", err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
