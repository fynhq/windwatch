package model

import "time"

const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

type Admin struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	Disabled     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (a Admin) IsAdmin() bool {
	return a.Role == RoleAdmin && !a.Disabled
}

type Session struct {
	TokenHash string
	AdminID   int64
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type AuthInfo struct {
	Admin   Admin
	Session Session
}
