package model

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultWarningReminderCount           = 0
	DefaultWarningReminderIntervalMinutes = 1440
	DefaultFailureReminderCount           = 0
	DefaultFailureReminderIntervalMinutes = 30
	MinNotificationReminders              = 0
	MinReminderIntervalMinutes            = 1
	MaxNotificationReminders              = 100
	MaxReminderIntervalMinutes            = 365 * 24 * 60
)

var (
	ErrReminderCount    = errors.New("invalid reminder count")
	ErrReminderInterval = errors.New("invalid reminder interval")
)

type NotificationPolicy struct {
	WarningReminderCount    int
	WarningReminderInterval time.Duration
	FailureReminderCount    int
	FailureReminderInterval time.Duration
}

func DefaultNotificationPolicy() NotificationPolicy {
	return NotificationPolicy{
		WarningReminderCount:    DefaultWarningReminderCount,
		WarningReminderInterval: DefaultWarningReminderIntervalMinutes * time.Minute,
		FailureReminderCount:    DefaultFailureReminderCount,
		FailureReminderInterval: DefaultFailureReminderIntervalMinutes * time.Minute,
	}
}

func (p NotificationPolicy) Normalized() NotificationPolicy {
	defaults := DefaultNotificationPolicy()
	if p.WarningReminderInterval <= 0 {
		p.WarningReminderInterval = defaults.WarningReminderInterval
	}
	if p.FailureReminderInterval <= 0 {
		p.FailureReminderInterval = defaults.FailureReminderInterval
	}
	return p
}

func (p NotificationPolicy) Validate() error {
	p = p.Normalized()
	if p.WarningReminderCount < 0 || p.WarningReminderCount > MaxNotificationReminders ||
		p.FailureReminderCount < 0 || p.FailureReminderCount > MaxNotificationReminders {
		return ErrReminderCount
	}
	if p.WarningReminderInterval < time.Minute || p.WarningReminderInterval > time.Duration(MaxReminderIntervalMinutes)*time.Minute ||
		p.FailureReminderInterval < time.Minute || p.FailureReminderInterval > time.Duration(MaxReminderIntervalMinutes)*time.Minute {
		return ErrReminderInterval
	}
	return nil
}

func ParseNotificationPolicy(warningCount, warningInterval, failureCount, failureInterval string) (NotificationPolicy, error) {
	defaults := DefaultNotificationPolicy()
	policy := defaults
	var err error
	policy.WarningReminderCount, err = parseReminderInt(warningCount, defaults.WarningReminderCount, MaxNotificationReminders, ErrReminderCount)
	if err != nil {
		return NotificationPolicy{}, err
	}
	policy.FailureReminderCount, err = parseReminderInt(failureCount, defaults.FailureReminderCount, MaxNotificationReminders, ErrReminderCount)
	if err != nil {
		return NotificationPolicy{}, err
	}
	warningMinutes, err := parseReminderInt(warningInterval, DefaultWarningReminderIntervalMinutes, MaxReminderIntervalMinutes, ErrReminderInterval)
	if err != nil {
		return NotificationPolicy{}, err
	}
	failureMinutes, err := parseReminderInt(failureInterval, DefaultFailureReminderIntervalMinutes, MaxReminderIntervalMinutes, ErrReminderInterval)
	if err != nil {
		return NotificationPolicy{}, err
	}
	policy.WarningReminderInterval = time.Duration(warningMinutes) * time.Minute
	policy.FailureReminderInterval = time.Duration(failureMinutes) * time.Minute
	if err := policy.Validate(); err != nil {
		return NotificationPolicy{}, err
	}
	return policy, nil
}

func parseReminderInt(value string, fallback, maxValue int, sentinel error) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > maxValue {
		return 0, sentinel
	}
	return parsed, nil
}

type Notification struct {
	Key            string
	Kind           EventType
	MonitorID      int64
	MonitorName    string
	MonitorTarget  string
	MonitorType    MonitorType
	Status         MonitorStatus
	Message        string
	OccurredAt     time.Time
	Downtime       time.Duration
	DowntimeStart  time.Time
	DowntimeCycles int
	Reminder       int
	ReminderTotal  int
	Cert           *CertificateInfo
}

type NotificationLog struct {
	ID            int64
	DedupeKey     string
	Channel       string
	ChannelID     int64
	ChannelName   string
	MonitorName   string
	Target        string
	Status        string
	Error         string
	Payload       string
	CreatedAt     time.Time
	SentAt        time.Time
	Attempts      int
	NextAttemptAt time.Time
}

type NotificationReminder struct {
	MonitorID   int64
	Kind        string
	StatusSince time.Time
	ContextKey  string
	SentCount   int
	NextAt      time.Time
	UpdatedAt   time.Time
}
