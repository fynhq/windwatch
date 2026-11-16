package model

import "time"

type NotificationOutboxEntry struct {
	ID            int64
	DedupeKey     string
	MonitorID     int64
	Payload       string
	CreatedAt     time.Time
	Attempts      int
	NextAttemptAt time.Time
}
