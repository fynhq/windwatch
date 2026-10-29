package scheduler

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/checker"
	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

type fakeNotificationSink struct {
	notifications []model.Notification
	enabled       bool
}

func (f *fakeNotificationSink) Enqueue(notification model.Notification) bool {
	f.notifications = append(f.notifications, notification)
	return true
}

func (f *fakeNotificationSink) NotificationsEnabled(context.Context) (bool, error) {
	return f.enabled, nil
}

func newNotificationTestScheduler(t *testing.T) (*Scheduler, *store.Store, *fakeNotificationSink, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "notifications.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	channelID, err := st.CreateNotificationChannel(t.Context(), model.NotificationChannel{
		Name: "Test webhook", Type: model.NotificationChannelWebhook, Enabled: true,
		WebhookURL: "https://example.com/hook",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(st, checker.NewRegistry(), logger)
	sink := &fakeNotificationSink{enabled: true}
	s.SetNotifier(sink)
	return s, st, sink, channelID
}

func TestApplyEnqueuesDownAndRecoveryNotifications(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	now := time.UnixMilli(time.Now().UTC().UnixMilli())
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	monitor.NotifyEnabled = true
	monitor.NotificationChannelIDs = []int64{channelID}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	monitor.ID = id

	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: false, ErrorMessage: "connection refused"},
		checkedAt: now,
	})
	if len(sink.notifications) != 1 {
		t.Fatalf("notifications after failure = %d, want 1", len(sink.notifications))
	}
	down := sink.notifications[0]
	if down.Kind != model.EventDown || down.Status != model.StatusDown ||
		down.MonitorID != id || down.MonitorName != monitor.Name ||
		down.MonitorTarget != "https://example.com" || down.Message != "connection refused" {
		t.Fatalf("unexpected DOWN notification: %+v", down)
	}
	if !strings.HasPrefix(down.Key, "down:") {
		t.Fatalf("DOWN key = %q", down.Key)
	}

	recoveredAt := now.Add(5 * time.Minute)
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true},
		checkedAt: recoveredAt,
	})
	if len(sink.notifications) != 2 {
		t.Fatalf("notifications after recovery = %d, want 2", len(sink.notifications))
	}
	up := sink.notifications[1]
	if up.Kind != model.EventUp || up.Status != model.StatusUp || up.Downtime != 5*time.Minute {
		t.Fatalf("unexpected UP notification: %+v", up)
	}
	if !up.DowntimeStart.Equal(now) || up.DowntimeCycles != 5 {
		t.Fatalf("UP downtime window = %+v, want start %s and 5 cycles", up, now)
	}
	if !strings.HasPrefix(up.Key, "up:") {
		t.Fatalf("UP key = %q", up.Key)
	}

	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true},
		checkedAt: recoveredAt.Add(time.Minute),
	})
	if len(sink.notifications) != 2 {
		t.Fatalf("notifications after steady success = %d, want 2", len(sink.notifications))
	}
}

func TestOutageCycles(t *testing.T) {
	tests := []struct {
		name        string
		downtime    time.Duration
		interval    time.Duration
		monitorType model.MonitorType
		want        int
	}{
		{name: "rounds up half interval", downtime: 90 * time.Second, interval: time.Minute, monitorType: model.MonitorTypeHTTP, want: 2},
		{name: "clamps to one cycle", downtime: 5 * time.Second, interval: time.Minute, monitorType: model.MonitorTypeHTTP, want: 1},
		{name: "push uses minute tick", downtime: 3 * time.Minute, interval: 5 * time.Minute, monitorType: model.MonitorTypePush, want: 3},
		{name: "zero downtime", downtime: 0, interval: time.Minute, monitorType: model.MonitorTypeHTTP, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := outageCycles(test.downtime, test.interval, test.monitorType); got != test.want {
				t.Fatalf("outageCycles(%s, %s, %s) = %d, want %d", test.downtime, test.interval, test.monitorType, got, test.want)
			}
		})
	}
}

func TestApplyDowntimeStartsAtFirstFailedCheck(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	now := time.Now().UTC().Truncate(time.Second)
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	monitor.NotifyEnabled = true
	monitor.FailThreshold = 2
	monitor.RecoverThreshold = 1
	monitor.Interval = time.Minute
	monitor.NotificationChannelIDs = []int64{channelID}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: false, ErrorMessage: "connection refused"},
		checkedAt: now,
	})
	if len(sink.notifications) != 0 {
		t.Fatalf("notifications before threshold = %d, want 0", len(sink.notifications))
	}

	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: false, ErrorMessage: "connection refused"},
		checkedAt: now.Add(time.Minute),
	})
	if len(sink.notifications) != 1 {
		t.Fatalf("notifications after threshold = %d, want 1", len(sink.notifications))
	}
	events, err := st.RecentEvents(t.Context(), id, 10)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(events) != 1 || !events[0].StartedAt.Equal(now) {
		t.Fatalf("DOWN event start = %+v, want %s", events, now)
	}

	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true},
		checkedAt: now.Add(2 * time.Minute),
	})
	if len(sink.notifications) != 2 {
		t.Fatalf("notifications after recovery = %d, want 2", len(sink.notifications))
	}
	up := sink.notifications[1]
	if up.Downtime != 2*time.Minute || !up.DowntimeStart.Equal(now) || up.DowntimeCycles != 2 {
		t.Fatalf("UP notification downtime = %+v, want start %s, 2m, 2 cycles", up, now)
	}
}

func TestApplyEnqueuesWarningNotificationsOnce(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	now := time.UnixMilli(time.Now().UTC().UnixMilli())

	certMonitor := model.Monitor{
		Name:                   "Certificate",
		Type:                   model.MonitorTypeHTTP,
		HTTP:                   &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299", CertCheck: true, CertWarnDays: 30},
		Interval:               time.Minute,
		Timeout:                time.Second,
		FailThreshold:          1,
		RecoverThreshold:       1,
		NotifyEnabled:          true,
		Enabled:                true,
		NotificationChannelIDs: []int64{channelID},
	}
	certID, err := st.CreateMonitor(t.Context(), certMonitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor cert: %v", err)
	}
	cert := &model.CertificateInfo{
		Fingerprint: "abc123",
		Issuer:      "Test CA",
		NotAfter:    now.Add(10 * 24 * time.Hour),
	}
	s.apply(t.Context(), outcome{
		monitorID: certID,
		result:    model.CheckResult{Success: true, Certificate: cert},
		checkedAt: now,
	})
	if len(sink.notifications) != 1 {
		t.Fatalf("certificate notifications = %d, want 1", len(sink.notifications))
	}
	warning := sink.notifications[0]
	if warning.Kind != model.EventWarnCert || warning.Status != model.StatusWarning || warning.Cert != cert {
		t.Fatalf("unexpected certificate warning: %+v", warning)
	}
	if warning.Key != "warn_cert:"+strconv.FormatInt(certID, 10)+":abc123:30" {
		t.Fatalf("certificate warning key = %q", warning.Key)
	}
	current, err := st.MonitorByID(t.Context(), certID)
	if err != nil || current.State.Status != model.StatusWarning {
		t.Fatalf("certificate state=%+v err=%v", current.State, err)
	}

	s.apply(t.Context(), outcome{
		monitorID: certID,
		result:    model.CheckResult{Success: true, Certificate: cert},
		checkedAt: now.Add(time.Minute),
	})
	if len(sink.notifications) != 1 {
		t.Fatalf("repeated warning notifications = %d, want 1", len(sink.notifications))
	}

	renewed := &model.CertificateInfo{
		Fingerprint: "def456",
		Issuer:      "Test CA",
		NotAfter:    now.Add(9 * 24 * time.Hour),
	}
	s.apply(t.Context(), outcome{
		monitorID: certID,
		result:    model.CheckResult{Success: true, Certificate: renewed},
		checkedAt: now.Add(2 * time.Minute),
	})
	if len(sink.notifications) != 2 || sink.notifications[1].Cert != renewed {
		t.Fatalf("renewed certificate notifications = %+v", sink.notifications)
	}

	domainMonitor := model.Monitor{
		Name:                   "Domain",
		Type:                   model.MonitorTypeHTTP,
		HTTP:                   &model.HTTPConfig{URL: "https://example.org", Method: "GET", ExpectedStatus: "200-299", DomainCheck: true, DomainWarnDays: 30},
		Interval:               time.Minute,
		Timeout:                time.Second,
		FailThreshold:          1,
		RecoverThreshold:       1,
		NotifyEnabled:          true,
		Enabled:                true,
		NotificationChannelIDs: []int64{channelID},
	}
	domainID, err := st.CreateMonitor(t.Context(), domainMonitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor domain: %v", err)
	}
	info := model.DomainInfo{
		Domain:    "example.org",
		Registrar: "Test Registrar",
		Statuses:  []string{"clientTransferProhibited"},
		ExpiresAt: now.Add(10 * 24 * time.Hour),
		FetchedAt: now,
	}
	if err := st.UpsertDomainInfo(t.Context(), info); err != nil {
		t.Fatalf("UpsertDomainInfo: %v", err)
	}
	s.apply(t.Context(), outcome{
		monitorID: domainID,
		result:    model.CheckResult{Success: true},
		checkedAt: now,
	})
	if len(sink.notifications) != 3 {
		t.Fatalf("domain notifications = %d, want 3", len(sink.notifications))
	}
	domainWarning := sink.notifications[2]
	if domainWarning.Kind != model.EventWarnDomain || domainWarning.Status != model.StatusWarning ||
		domainWarning.Domain == nil || domainWarning.Domain.Domain != "example.org" {
		t.Fatalf("unexpected domain warning: %+v", domainWarning)
	}
	if !strings.HasPrefix(domainWarning.Key, "warn_domain:example.org:") {
		t.Fatalf("domain warning key = %q", domainWarning.Key)
	}
}

func TestDisabledChannelsDoNotConsumeReminders(t *testing.T) {
	s, st, sink, channelID := newNotificationTestScheduler(t)
	now := time.UnixMilli(time.Now().UTC().UnixMilli())
	monitor := schedulerTestMonitor("https://example.com")
	monitor.Enabled = true
	monitor.NotifyEnabled = true
	monitor.NotificationChannelIDs = []int64{channelID}
	monitor.Reminders = model.NotificationPolicy{
		FailureReminderCount:    1,
		FailureReminderInterval: time.Minute,
	}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	sink.enabled = false
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: false, ErrorMessage: "connection refused"},
		checkedAt: now,
	})
	if len(sink.notifications) != 0 {
		t.Fatalf("notifications while disabled = %d", len(sink.notifications))
	}

	sink.enabled = true
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: false, ErrorMessage: "connection refused"},
		checkedAt: now.Add(2 * time.Minute),
	})
	if len(sink.notifications) != 1 || sink.notifications[0].Reminder != 1 {
		t.Fatalf("notifications after enabling = %+v", sink.notifications)
	}
}
