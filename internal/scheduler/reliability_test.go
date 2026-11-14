package scheduler

import (
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestWarningStatusSinceStableWithoutNotificationChannels(t *testing.T) {
	s, st, _, _ := newNotificationTestScheduler(t)
	now := time.UnixMilli(time.Now().UTC().UnixMilli())
	monitor := model.Monitor{
		Name: "Cert without channels", Type: model.MonitorTypeHTTP,
		HTTP: &model.HTTPConfig{
			URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299",
			CertCheck: true, CertWarnDays: 30,
		},
		Interval: time.Minute, Timeout: time.Second,
		FailThreshold: 1, RecoverThreshold: 1,
		NotifyEnabled: true, Enabled: true,
	}
	id, err := st.CreateMonitor(t.Context(), monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	cert := &model.CertificateInfo{Fingerprint: "fp-stable", NotAfter: now.Add(10 * 24 * time.Hour)}

	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true, Certificate: cert},
		checkedAt: now,
	})
	first, err := st.MonitorByID(t.Context(), id)
	if err != nil || first.State.Status != model.StatusWarning {
		t.Fatalf("first state=%+v err=%v", first.State, err)
	}
	s.apply(t.Context(), outcome{
		monitorID: id,
		result:    model.CheckResult{Success: true, Certificate: cert},
		checkedAt: now.Add(time.Minute),
	})
	second, err := st.MonitorByID(t.Context(), id)
	if err != nil || second.State.Status != model.StatusWarning {
		t.Fatalf("second state=%+v err=%v", second.State, err)
	}
	if !second.State.StatusSince.Equal(first.State.StatusSince) {
		t.Fatalf("warning status_since changed without channels: first=%s second=%s", first.State.StatusSince, second.State.StatusSince)
	}
	if second.State.WarningContext == "" {
		t.Fatal("warning context was not persisted without notification channels")
	}
}

func TestPushResumeStartsFreshHeartbeatWindow(t *testing.T) {
	s, st, _, _ := newNotificationTestScheduler(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	monitor := model.Monitor{
		Name: "Push resume", Type: model.MonitorTypePush,
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1,
		NotifyEnabled: true, Enabled: true,
	}
	id, err := st.CreateMonitorWithPushToken(t.Context(), monitor, now, "hash", "enc")
	if err != nil {
		t.Fatalf("CreateMonitorWithPushToken: %v", err)
	}
	if err := st.RecordHeartbeat(t.Context(), id, now.Add(-time.Hour)); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	if err := s.SetMonitorEnabled(t.Context(), id, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := s.SetMonitorEnabled(t.Context(), id, true); err != nil {
		t.Fatalf("resume: %v", err)
	}
	token, err := st.PushTokenByMonitorID(t.Context(), id)
	if err != nil {
		t.Fatalf("PushTokenByMonitorID: %v", err)
	}
	if token.LastHeartbeat.Before(now) {
		t.Fatalf("resume did not refresh push heartbeat: old=%s current=%s", now.Add(-time.Hour), token.LastHeartbeat)
	}
}
