package model

import (
	"strconv"
	"testing"
	"time"
)

func TestNotificationKeyGrammar(t *testing.T) {
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	down := StateNotificationKey(NotificationKindDown, 12, at)
	if down != "down:12:"+itoaMillis(at) {
		t.Fatalf("down key = %q", down)
	}
	if id, ok := StateNotificationMonitorID(down); !ok || id != 12 {
		t.Fatalf("parse down = %d,%v", id, ok)
	}
	if reason := NotificationReason(down); reason != NotificationReasonDown {
		t.Fatalf("down reason = %q", reason)
	}
	up := StateNotificationKey(NotificationKindUp, 3, at)
	if reason := NotificationReason(up); reason != NotificationReasonRecovery {
		t.Fatalf("up reason = %q", reason)
	}
	if _, ok := StateNotificationMonitorID(WarningReminderKey(7, "cert:fp:30", at, 1)); ok {
		t.Fatal("warning reminders must not participate in state ordering")
	}
	if reason := NotificationReason(WarningReminderKey(7, "cert:fp:30", at, 1)); reason != NotificationReasonWarning {
		t.Fatalf("warning reason = %q", reason)
	}
	if _, ok := StateNotificationMonitorID("down::1"); ok {
		t.Fatal("empty monitor id must not parse")
	}
}

func itoaMillis(value time.Time) string {
	return strconv.FormatInt(value.UnixMilli(), 10)
}
