package store

import (
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestRecordCheckWithNotificationsIsAtomic(t *testing.T) {
	st := newStoreTest(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	monitorID, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "Atomic", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: time.Second, FailThreshold: 1, RecoverThreshold: 1,
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	notification := model.Notification{
		Key:  model.StateNotificationKey(model.NotificationKindDown, monitorID, now),
		Kind: model.EventDown, MonitorID: monitorID, MonitorName: "Atomic",
		MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "boom", OccurredAt: now,
	}
	warningContext := "cert:fp:30"
	event := &model.Event{
		MonitorID: monitorID, Type: model.EventDown, Message: "boom",
		StartedAt: now, MetaJSON: "{}",
	}
	state := model.MonitorState{
		Status: model.StatusWarning, StatusSince: now, LastCheck: now,
		LastError: "boom", ConsecutiveFailures: 1, UpdatedAt: now,
	}
	if err := st.RecordCheckWithNotifications(t.Context(), model.Check{
		MonitorID: monitorID, CheckedAt: now, Status: model.CheckWarning, Latency: time.Millisecond, MetaJSON: "{}",
	}, state, event, false, []model.Notification{notification}, &warningContext); err != nil {
		t.Fatalf("RecordCheckWithNotifications: %v", err)
	}
	current, err := st.MonitorByID(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if current.State.WarningContext != warningContext {
		t.Fatalf("warning context = %q, want %q", current.State.WarningContext, warningContext)
	}
	entries, err := st.DueNotificationOutbox(t.Context(), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("DueNotificationOutbox: %v", err)
	}
	if len(entries) != 1 || entries[0].DedupeKey != notification.Key {
		t.Fatalf("outbox entries = %+v", entries)
	}
	events, err := st.RecentEvents(t.Context(), monitorID, 10)
	if err != nil || len(events) != 1 || events[0].Type != model.EventDown {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestSetMonitorEnabledWithResetKeepsUninitializedPushPending(t *testing.T) {
	st := newStoreTest(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	monitorID, err := st.CreateMonitorWithPushToken(t.Context(), model.Monitor{
		Name: "Push no heartbeat", Type: model.MonitorTypePush,
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1, Enabled: true,
	}, now, "hash", "enc")
	if err != nil {
		t.Fatalf("CreateMonitorWithPushToken: %v", err)
	}
	if err := st.SetMonitorEnabledWithReset(t.Context(), monitorID, false, now); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := st.SetMonitorEnabledWithReset(t.Context(), monitorID, true, now.Add(time.Minute)); err != nil {
		t.Fatalf("resume: %v", err)
	}
	token, err := st.PushTokenByMonitorID(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("PushTokenByMonitorID: %v", err)
	}
	if !token.LastHeartbeat.IsZero() {
		t.Fatalf("resume manufactured a heartbeat for a monitor that never reported one: %s", token.LastHeartbeat)
	}
	current, err := st.MonitorByID(t.Context(), monitorID)
	if err != nil || current.State.Status != model.StatusPending {
		t.Fatalf("state=%+v err=%v", current.State, err)
	}
}

func TestAggregateHalfHourlyRollupsDoesNotOverwritePartialBuckets(t *testing.T) {
	st := newStoreTest(t)
	now := time.Date(2026, 11, 14, 13, 0, 0, 0, time.UTC)
	bucket := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	monitorID, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "Rollup alignment", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: time.Second, FailThreshold: 1, RecoverThreshold: 1,
	}, bucket)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	for _, at := range []time.Time{bucket.Add(5 * time.Minute), bucket.Add(10 * time.Minute)} {
		if err := st.RecordCheck(t.Context(), model.Check{
			MonitorID: monitorID, CheckedAt: at, Status: model.CheckUp, Latency: time.Millisecond, MetaJSON: "{}",
		}, model.MonitorState{Status: model.StatusUp, StatusSince: at, LastCheck: at, UpdatedAt: at}, nil, false); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
	}
	if err := st.AggregateHalfHourlyRollups(t.Context(), bucket, bucket.Add(30*time.Minute), now); err != nil {
		t.Fatalf("full aggregate: %v", err)
	}
	stats, err := st.RollupStatsForMonitors(t.Context(), []int64{monitorID}, bucket, bucket.Add(30*time.Minute))
	if err != nil || stats[monitorID].Total != 2 {
		t.Fatalf("full rollup total=%d err=%v", stats[monitorID].Total, err)
	}
	// A mid-bucket call must normalize to a complete bucket (here a no-op)
	// instead of replacing the existing count with a partial count.
	if err := st.AggregateHalfHourlyRollups(t.Context(), bucket.Add(7*time.Minute), bucket.Add(20*time.Minute), now); err != nil {
		t.Fatalf("partial aggregate: %v", err)
	}
	stats, err = st.RollupStatsForMonitors(t.Context(), []int64{monitorID}, bucket, bucket.Add(30*time.Minute))
	if err != nil || stats[monitorID].Total != 2 {
		t.Fatalf("partial call changed rollup total=%d err=%v", stats[monitorID].Total, err)
	}
}
