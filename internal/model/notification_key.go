package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	NotificationKindDown          = "down"
	NotificationKindUp            = "up"
	NotificationKindWarnCert      = "warn_cert"
	NotificationKindWarnDomain    = "warn_domain"
	NotificationKindRemindDown    = "remind_down"
	NotificationKindRemindWarning = "remind_warning"
)

const (
	NotificationReasonRecovery = "recovery"
	NotificationReasonDown     = "down"
	NotificationReasonWarning  = "warning"
)

func StateNotificationKey(kind string, monitorID int64, at time.Time) string {
	return fmt.Sprintf("%s:%d:%d", kind, monitorID, at.UnixMilli())
}

func WarnCertNotificationKey(monitorID int64, fingerprint string, warnDays int, episodeStart time.Time) string {
	return fmt.Sprintf("%s:%d:%s:%d:%d", NotificationKindWarnCert, monitorID, fingerprint, warnDays, episodeStart.UnixMilli())
}

func DownReminderKey(monitorID int64, statusSince time.Time, sequence int) string {
	return fmt.Sprintf("%s:%d:%d:%d", NotificationKindRemindDown, monitorID, statusSince.UnixMilli(), sequence)
}

func WarningReminderKey(monitorID int64, contextKey string, statusSince time.Time, sequence int) string {
	return fmt.Sprintf("%s:%d:%s:%d:%d", NotificationKindRemindWarning, monitorID, contextKey, statusSince.UnixMilli(), sequence)
}

func NotificationReason(key string) string {
	switch {
	case strings.HasPrefix(key, NotificationKindUp+":"):
		return NotificationReasonRecovery
	case strings.HasPrefix(key, NotificationKindDown+":"),
		strings.HasPrefix(key, NotificationKindRemindDown+":"):
		return NotificationReasonDown
	case strings.HasPrefix(key, NotificationKindWarnCert+":"),
		strings.HasPrefix(key, NotificationKindWarnDomain+":"),
		strings.HasPrefix(key, NotificationKindRemindWarning+":"):
		return NotificationReasonWarning
	default:
		return ""
	}
}

func StateNotificationMonitorID(key string) (int64, bool) {
	kind, rest, ok := strings.Cut(key, ":")
	if !ok {
		return 0, false
	}
	switch kind {
	case NotificationKindDown, NotificationKindUp, NotificationKindRemindDown:
	default:
		return 0, false
	}
	idText, _, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func StateNotificationLikePatterns(monitorID int64) []string {
	id := strconv.FormatInt(monitorID, 10)
	return []string{
		NotificationKindDown + ":" + id + ":%",
		NotificationKindUp + ":" + id + ":%",
		NotificationKindRemindDown + ":" + id + ":%",
	}
}

func NotificationLikePatternsForMonitor(monitorID int64) []string {
	patterns := StateNotificationLikePatterns(monitorID)
	id := strconv.FormatInt(monitorID, 10)
	return append(patterns,
		NotificationKindWarnCert+":"+id+":%",
		NotificationKindRemindWarning+":"+id+":%",
	)
}
