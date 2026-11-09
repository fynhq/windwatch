package model

import "time"

// NotificationOutboxEntry is a durable notification intent. The payload is
// serialized at enqueue time so a restart can recover it without rebuilding
// monitor state.
type NotificationOutboxEntry struct {
	ID            int64
	DedupeKey     string
	MonitorID     int64
	Payload       string
	CreatedAt     time.Time
	Attempts      int
	NextAttemptAt time.Time
}
