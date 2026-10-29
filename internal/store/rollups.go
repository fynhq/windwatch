package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

const HalfHourlyResolutionSeconds = 1800

func (s *Store) AggregateHalfHourlyRollups(ctx context.Context, since, before, now time.Time) error {
	if !before.After(since) {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rollups (
			monitor_id, resolution_seconds, bucket_start_ms,
			total, up, warning, down, latency_sum_ms, updated_at_ms
		)
		SELECT monitor_id,
		       ?,
		       (checked_at_ms / 1800000) * 1800000,
		       COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'up' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'warning' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'down' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(latency_ms), 0),
		       ?
		FROM checks
		WHERE checked_at_ms >= ? AND checked_at_ms < ?
		GROUP BY monitor_id, (checked_at_ms / 1800000) * 1800000
		ON CONFLICT(monitor_id, resolution_seconds, bucket_start_ms) DO UPDATE SET
			total = excluded.total,
			up = excluded.up,
			warning = excluded.warning,
			down = excluded.down,
			latency_sum_ms = excluded.latency_sum_ms,
			updated_at_ms = excluded.updated_at_ms
	`,
		HalfHourlyResolutionSeconds, now.UTC().UnixMilli(),
		since.UTC().UnixMilli(), before.UTC().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("aggregate half-hourly rollups: %w", err)
	}
	return nil
}

func (s *Store) LastRollupStart(ctx context.Context) (time.Time, bool, error) {
	var bucket sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT MAX(bucket_start_ms) FROM rollups WHERE resolution_seconds = ?
	`, HalfHourlyResolutionSeconds).Scan(&bucket); err != nil {
		return time.Time{}, false, fmt.Errorf("last rollup: %w", err)
	}
	if !bucket.Valid || bucket.Int64 == 0 {
		return time.Time{}, false, nil
	}
	return time.UnixMilli(bucket.Int64).UTC(), true, nil
}

func (s *Store) RollupStats(ctx context.Context, monitorID int64, since, before time.Time) (model.UptimeStats, error) {
	var (
		stats      model.UptimeStats
		latencySum int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total), 0),
		       COALESCE(SUM(up), 0),
		       COALESCE(SUM(warning), 0),
		       COALESCE(SUM(down), 0),
		       COALESCE(SUM(latency_sum_ms), 0)
		FROM rollups
		WHERE monitor_id = ? AND resolution_seconds = ?
		  AND bucket_start_ms >= ? AND bucket_start_ms < ?
	`, monitorID, HalfHourlyResolutionSeconds, since.UTC().UnixMilli(), before.UTC().UnixMilli()).Scan(
		&stats.Total, &stats.Up, &stats.Warning, &stats.Down, &latencySum,
	)
	if err != nil {
		return model.UptimeStats{}, fmt.Errorf("get rollup stats: %w", err)
	}
	if stats.Total > 0 {
		stats.AvgLatency = time.Duration(latencySum/int64(stats.Total)) * time.Millisecond
	}
	return stats, nil
}

func (s *Store) RollupStatsForMonitors(ctx context.Context, monitorIDs []int64, since, before time.Time) (map[int64]model.UptimeStats, error) {
	result := make(map[int64]model.UptimeStats, len(monitorIDs))
	if len(monitorIDs) == 0 {
		return result, nil
	}
	args := make([]any, 0, len(monitorIDs)+3)
	for _, id := range monitorIDs {
		args = append(args, id)
	}
	args = append(args, HalfHourlyResolutionSeconds, since.UTC().UnixMilli(), before.UTC().UnixMilli())
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id,
		       COALESCE(SUM(total), 0),
		       COALESCE(SUM(up), 0),
		       COALESCE(SUM(warning), 0),
		       COALESCE(SUM(down), 0),
		       COALESCE(SUM(latency_sum_ms), 0)
		FROM rollups
		WHERE monitor_id IN (`+idPlaceholders(len(monitorIDs))+`) AND resolution_seconds = ?
		  AND bucket_start_ms >= ? AND bucket_start_ms < ?
		GROUP BY monitor_id
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("get rollup stats for monitors: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			monitorID  int64
			stats      model.UptimeStats
			latencySum int64
		)
		if err := rows.Scan(&monitorID, &stats.Total, &stats.Up, &stats.Warning, &stats.Down, &latencySum); err != nil {
			return nil, fmt.Errorf("scan rollup stats: %w", err)
		}
		if stats.Total > 0 {
			stats.AvgLatency = time.Duration(latencySum/int64(stats.Total)) * time.Millisecond
		}
		result[monitorID] = stats
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rollup stats: %w", err)
	}
	return result, nil
}

func (s *Store) HalfHourlyBuckets(ctx context.Context, monitorID int64, since, before time.Time) ([]model.HourBucket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT bucket_start_ms, total, up, warning, down, latency_sum_ms
		FROM rollups
		WHERE monitor_id = ? AND resolution_seconds = ?
		  AND bucket_start_ms >= ? AND bucket_start_ms < ?
		ORDER BY bucket_start_ms ASC
	`, monitorID, HalfHourlyResolutionSeconds, since.UTC().UnixMilli(), before.UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("get half-hourly buckets: %w", err)
	}
	defer rows.Close()

	var buckets []model.HourBucket
	for rows.Next() {
		var (
			bucket     model.HourBucket
			bucketMS   int64
			latencySum int64
		)
		if err := rows.Scan(&bucketMS, &bucket.Total, &bucket.Up, &bucket.Warning, &bucket.Down, &latencySum); err != nil {
			return nil, fmt.Errorf("scan half-hourly bucket: %w", err)
		}
		bucket.MonitorID = monitorID
		bucket.Start = time.UnixMilli(bucketMS).UTC()
		if bucket.Total > 0 {
			bucket.AvgLatency = time.Duration(latencySum/int64(bucket.Total)) * time.Millisecond
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate half-hourly buckets: %w", err)
	}
	return buckets, nil
}

func (s *Store) DailyBucketsForTimezone(ctx context.Context, monitorID int64, since, before time.Time, location *time.Location) ([]model.HourBucket, error) {
	if location == nil {
		location = time.UTC
	}
	buckets, err := s.HalfHourlyBuckets(ctx, monitorID, since, before)
	if err != nil {
		return nil, err
	}
	type aggregate struct {
		bucket     model.HourBucket
		latencySum int64
	}
	byDay := make(map[int64]*aggregate)
	for _, bucket := range buckets {
		local := bucket.Start.In(location)
		dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
		key := dayStart.UnixMilli()
		entry := byDay[key]
		if entry == nil {
			entry = &aggregate{bucket: model.HourBucket{MonitorID: monitorID, Start: dayStart}}
			byDay[key] = entry
		}
		entry.bucket.Total += bucket.Total
		entry.bucket.Up += bucket.Up
		entry.bucket.Warning += bucket.Warning
		entry.bucket.Down += bucket.Down
		entry.latencySum += int64(bucket.AvgLatency/time.Millisecond) * int64(bucket.Total)
	}
	keys := make([]int64, 0, len(byDay))
	for key := range byDay {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	daily := make([]model.HourBucket, 0, len(keys))
	for _, key := range keys {
		entry := byDay[key]
		if entry.bucket.Total > 0 {
			entry.bucket.AvgLatency = time.Duration(entry.latencySum/int64(entry.bucket.Total)) * time.Millisecond
		}
		daily = append(daily, entry.bucket)
	}
	return daily, nil
}

func (s *Store) DailyBucketsForMonitors(ctx context.Context, monitorIDs []int64, since, before time.Time, location *time.Location) (map[int64][]model.HourBucket, error) {
	result := make(map[int64][]model.HourBucket, len(monitorIDs))
	if len(monitorIDs) == 0 {
		return result, nil
	}
	if location == nil {
		location = time.UTC
	}
	args := make([]any, 0, len(monitorIDs)+3)
	for _, id := range monitorIDs {
		args = append(args, id)
	}
	args = append(args, HalfHourlyResolutionSeconds, since.UTC().UnixMilli(), before.UTC().UnixMilli())
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id, bucket_start_ms, total, up, warning, down, latency_sum_ms
		FROM rollups
		WHERE monitor_id IN (`+idPlaceholders(len(monitorIDs))+`) AND resolution_seconds = ?
		  AND bucket_start_ms >= ? AND bucket_start_ms < ?
		ORDER BY monitor_id, bucket_start_ms
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("get daily buckets for monitors: %w", err)
	}
	defer rows.Close()

	type aggregate struct {
		bucket     model.HourBucket
		latencySum int64
	}
	byMonitor := make(map[int64]map[int64]*aggregate, len(monitorIDs))
	for rows.Next() {
		var (
			monitorID  int64
			bucketMS   int64
			total      int
			up         int
			warning    int
			down       int
			latencySum int64
		)
		if err := rows.Scan(&monitorID, &bucketMS, &total, &up, &warning, &down, &latencySum); err != nil {
			return nil, fmt.Errorf("scan daily bucket for monitors: %w", err)
		}
		local := time.UnixMilli(bucketMS).In(location)
		dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
		key := dayStart.UnixMilli()
		days := byMonitor[monitorID]
		if days == nil {
			days = make(map[int64]*aggregate)
			byMonitor[monitorID] = days
		}
		entry := days[key]
		if entry == nil {
			entry = &aggregate{bucket: model.HourBucket{MonitorID: monitorID, Start: dayStart}}
			days[key] = entry
		}
		entry.bucket.Total += total
		entry.bucket.Up += up
		entry.bucket.Warning += warning
		entry.bucket.Down += down
		entry.latencySum += latencySum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily buckets for monitors: %w", err)
	}

	for monitorID, days := range byMonitor {
		keys := make([]int64, 0, len(days))
		for key := range days {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		daily := make([]model.HourBucket, 0, len(keys))
		for _, key := range keys {
			entry := days[key]
			if entry.bucket.Total > 0 {
				entry.bucket.AvgLatency = time.Duration(entry.latencySum/int64(entry.bucket.Total)) * time.Millisecond
			}
			daily = append(daily, entry.bucket)
		}
		result[monitorID] = daily
	}
	return result, nil
}

func (s *Store) DeleteRollupsBefore(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM rollups WHERE resolution_seconds = ? AND bucket_start_ms < ?
	`, HalfHourlyResolutionSeconds, before.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("delete old rollups: %w", err)
	}
	return nil
}
