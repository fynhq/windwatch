package scheduler

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/checker"
	"github.com/fynhq/windwatch/internal/model"
)

func TestFailureReminderSchedule(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	now := time.UnixMilli(time.Now().UTC().UnixMilli())
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	monitor.NotifyEnabled = true
	monitor.NotificationChannelIDs = []int64{channelID}
	monitor.Reminders = model.NotificationPolicy{
		FailureReminderCount:    2,
		FailureReminderInterval: 30 * time.Minute,
	}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	applyFailure := func(scheduler *Scheduler, at time.Time) {
		scheduler.apply(t.Context(), outcome{
			monitorID: id,
			result:    model.CheckResult{Success: false, ErrorMessage: "connection refused"},
			checkedAt: at,
		})
	}
	applyFailure(s, now)
	if len(sink.notifications) != 1 {
		t.Fatalf("initial down notifications = %d, want 1", len(sink.notifications))
	}
	applyFailure(s, now.Add(30*time.Minute))
	if len(sink.notifications) != 2 {
		t.Fatalf("first reminder notifications = %d, want 2", len(sink.notifications))
	}
	first := sink.notifications[1]
	if first.Reminder != 1 || first.ReminderTotal != 2 || first.Kind != model.EventDown ||
		!strings.HasPrefix(first.Key, "remind_down:") || first.Message != "connection refused" {
		t.Fatalf("unexpected first reminder: %+v", first)
	}
	applyFailure(s, now.Add(60*time.Minute))
	if len(sink.notifications) != 3 {
		t.Fatalf("second reminder notifications = %d, want 3", len(sink.notifications))
	}
	if sink.notifications[2].Reminder != 2 {
		t.Fatalf("second reminder = %+v", sink.notifications[2])
	}
	applyFailure(s, now.Add(90*time.Minute))
	if len(sink.notifications) != 3 {
		t.Fatalf("extra reminder notifications = %d, want 3", len(sink.notifications))
	}

	restarted := New(st, checker.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	restarted.SetNotifier(sink)
	applyFailure(restarted, now.Add(120*time.Minute))
	if len(sink.notifications) != 3 {
		t.Fatalf("reminder after restart = %d, want 3", len(sink.notifications))
	}
	state, found, err := st.ReminderState(t.Context(), id, "down")
	if err != nil || !found || state.SentCount != 2 {
		t.Fatalf("reminder state = %+v found=%v err=%v", state, found, err)
	}
}

func TestWarningReminderSchedule(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	now := time.UnixMilli(time.Now().UTC().UnixMilli())
	monitor := model.Monitor{
		Name: "Certificate", Type: model.MonitorTypeHTTP,
		HTTP: &model.HTTPConfig{
			URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299",
			CertCheck: true, CertWarnDays: 30,
		},
		Interval: time.Minute, Timeout: time.Second,
		FailThreshold: 1, RecoverThreshold: 1, NotifyEnabled: true, Enabled: true,
		NotificationChannelIDs: []int64{channelID},
		Reminders: model.NotificationPolicy{
			WarningReminderCount:    2,
			WarningReminderInterval: 60 * time.Minute,
		},
	}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	cert := &model.CertificateInfo{
		Fingerprint: "fp-reminder", Issuer: "Test CA", NotAfter: now.Add(10 * 24 * time.Hour),
	}
	applyWarning := func(scheduler *Scheduler, at time.Time) {
		scheduler.apply(t.Context(), outcome{
			monitorID: id,
			result:    model.CheckResult{Success: true, Certificate: cert},
			checkedAt: at,
		})
	}
	applyWarning(s, now)
	if len(sink.notifications) != 1 {
		t.Fatalf("initial warning notifications = %d, want 1", len(sink.notifications))
	}
	applyWarning(s, now.Add(time.Hour))
	if len(sink.notifications) != 2 {
		t.Fatalf("first warning reminder notifications = %d, want 2", len(sink.notifications))
	}
	first := sink.notifications[1]
	if first.Reminder != 1 || first.ReminderTotal != 2 || first.Kind != model.EventWarnCert ||
		!strings.HasPrefix(first.Key, "remind_warning:") || first.Cert == nil {
		t.Fatalf("unexpected warning reminder: %+v", first)
	}
	applyWarning(s, now.Add(2*time.Hour))
	if len(sink.notifications) != 3 || sink.notifications[2].Reminder != 2 {
		t.Fatalf("second warning reminder notifications=%d last=%+v", len(sink.notifications), sink.notifications[len(sink.notifications)-1])
	}
	applyWarning(s, now.Add(3*time.Hour))
	if len(sink.notifications) != 3 {
		t.Fatalf("extra warning reminders = %d, want 3", len(sink.notifications))
	}
	current, err := st.MonitorByID(t.Context(), id)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if current.State.Status != model.StatusWarning || current.State.StatusSince.UnixMilli() != now.UnixMilli() {
		t.Fatalf("warning state = %+v", current.State)
	}
}
