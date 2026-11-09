package model

import "time"

type PushToken struct {
	MonitorID     int64
	TokenHash     string
	TokenEnc      string
	LastHeartbeat time.Time
	CreatedAt     time.Time
}
