package model

import "time"

type PushToken struct {
	MonitorID     int64
	TokenHash     string
	TokenEnc      string
	LastHeartbeat time.Time
	CreatedAt     time.Time
}

type DomainInfo struct {
	Domain    string
	Registrar string
	Statuses  []string
	ExpiresAt time.Time
	FetchedAt time.Time
	Error     string
}
