package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func createStoreHTTPMonitor(t *testing.T, st *Store, name string) int64 {
	t.Helper()
	now := time.Now().UTC()
	id, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: name, Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: time.Second, FailThreshold: 1, RecoverThreshold: 1,
		Enabled: true, NotifyEnabled: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return id
}

func TestPrunePublicStatusMonitorIDs(t *testing.T) {
	st := newStoreTest(t)
	first := createStoreHTTPMonitor(t, st, "First")
	second := createStoreHTTPMonitor(t, st, "Second")
	raw, err := json.Marshal([]int64{first, second, 999, second, 0})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	now := time.Now().UTC()
	if err := st.SetSettings(t.Context(), map[string]string{
		model.SettingPublicStatusMonitorIDs: string(raw),
	}, now); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if err := st.PrunePublicStatusMonitorIDs(t.Context(), now); err != nil {
		t.Fatalf("PrunePublicStatusMonitorIDs: %v", err)
	}
	values, err := st.Settings(t.Context())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	var ids []int64
	if err := json.Unmarshal([]byte(values[model.SettingPublicStatusMonitorIDs]), &ids); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(ids) != 2 || ids[0] != first || ids[1] != second {
		t.Fatalf("pruned ids = %+v", ids)
	}

	if err := st.DeleteMonitor(t.Context(), second); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	if err := st.PrunePublicStatusMonitorIDs(t.Context(), now); err != nil {
		t.Fatalf("Prune after delete: %v", err)
	}
	values, err = st.Settings(t.Context())
	if err != nil {
		t.Fatalf("Settings after delete: %v", err)
	}
	if err := json.Unmarshal([]byte(values[model.SettingPublicStatusMonitorIDs]), &ids); err != nil {
		t.Fatalf("Unmarshal after delete: %v", err)
	}
	if len(ids) != 1 || ids[0] != first {
		t.Fatalf("ids after delete = %+v", ids)
	}
}

func TestMonitorIDsAreNotReused(t *testing.T) {
	st := newStoreTest(t)
	first := createStoreHTTPMonitor(t, st, "First")
	second := createStoreHTTPMonitor(t, st, "Second")
	if second <= first {
		t.Fatalf("ids are not increasing: %d, %d", first, second)
	}
	if err := st.DeleteMonitor(t.Context(), second); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	third := createStoreHTTPMonitor(t, st, "Third")
	if third <= second {
		t.Fatalf("reused monitor id %d after deleting %d", third, second)
	}
}

func TestOpenAppliesConnectionPragmas(t *testing.T) {
	st := newStoreTest(t)
	var foreignKeys, busyTimeout int
	if err := st.db.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if err := st.db.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d", foreignKeys)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d", busyTimeout)
	}
	var encryptedColumns int
	if err := st.db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM pragma_table_info('settings') WHERE name = 'encrypted'").Scan(&encryptedColumns); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	if encryptedColumns != 0 {
		t.Fatal("settings.encrypted was not removed")
	}
}

func TestDeleteNotificationsBefore(t *testing.T) {
	st := newStoreTest(t)
	now := time.Now().UTC()
	if ok, err := st.ClaimNotification(t.Context(), "old-sent", "webhook", 1, "Old hook", "webhook", "old", "", now.Add(-2*time.Hour)); err != nil || !ok {
		t.Fatalf("claim old sent: ok=%v err=%v", ok, err)
	}
	if err := st.CompleteNotification(t.Context(), "old-sent", "webhook", 1, "webhook", "sent", "", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("complete old sent: %v", err)
	}
	if ok, err := st.ClaimNotification(t.Context(), "old-pending", "webhook", 1, "Old hook", "webhook", "pending", "", now.Add(-2*time.Hour)); err != nil || !ok {
		t.Fatalf("claim old pending: ok=%v err=%v", ok, err)
	}
	if ok, err := st.ClaimNotification(t.Context(), "new-sent", "webhook", 1, "Old hook", "webhook", "new", "", now); err != nil || !ok {
		t.Fatalf("claim new sent: ok=%v err=%v", ok, err)
	}
	if err := st.CompleteNotification(t.Context(), "new-sent", "webhook", 1, "webhook", "sent", "", now); err != nil {
		t.Fatalf("complete new sent: %v", err)
	}

	if err := st.DeleteNotificationsBefore(t.Context(), now.Add(-time.Hour)); err != nil {
		t.Fatalf("DeleteNotificationsBefore: %v", err)
	}
	var oldSent, oldPending, newSent int
	if err := st.db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM notify_log WHERE dedupe_key = 'old-sent'").Scan(&oldSent); err != nil {
		t.Fatalf("count old sent: %v", err)
	}
	if err := st.db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM notify_log WHERE dedupe_key = 'old-pending'").Scan(&oldPending); err != nil {
		t.Fatalf("count old pending: %v", err)
	}
	if err := st.db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM notify_log WHERE dedupe_key = 'new-sent'").Scan(&newSent); err != nil {
		t.Fatalf("count new sent: %v", err)
	}
	if oldSent != 0 || oldPending != 1 || newSent != 1 {
		t.Fatalf("counts oldSent=%d oldPending=%d newSent=%d", oldSent, oldPending, newSent)
	}
}

func TestCloseAndEnsureOpenDownEvent(t *testing.T) {
	st := newStoreTest(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	id := createStoreHTTPMonitor(t, st, "Down")
	state := model.MonitorState{
		Status: model.StatusDown, StatusSince: now, LastCheck: now, LastError: "boom", UpdatedAt: now,
	}
	event := &model.Event{MonitorID: id, Type: model.EventDown, StartedAt: now, MetaJSON: "{}"}
	if err := st.RecordCheck(t.Context(), model.Check{
		MonitorID: id, CheckedAt: now, Status: model.CheckDown, ErrorKind: model.ErrorKindNetwork,
		ErrorMessage: "boom", MetaJSON: "{}",
	}, state, event, false); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if err := st.CloseOpenDownEvents(t.Context(), id, now.Add(time.Minute)); err != nil {
		t.Fatalf("CloseOpenDownEvents: %v", err)
	}
	events, err := st.RecentEvents(t.Context(), id, 10)
	if err != nil || len(events) != 1 || events[0].EndedAt == nil {
		t.Fatalf("events after close = %+v err=%v", events, err)
	}
	if err := st.EnsureOpenDownEvent(t.Context(), id, now.Add(2*time.Minute), "boom"); err != nil {
		t.Fatalf("EnsureOpenDownEvent: %v", err)
	}
	if err := st.EnsureOpenDownEvent(t.Context(), id, now.Add(3*time.Minute), "boom"); err != nil {
		t.Fatalf("EnsureOpenDownEvent second: %v", err)
	}
	events, err = st.RecentEvents(t.Context(), id, 10)
	if err != nil || len(events) != 2 || events[0].EndedAt != nil {
		t.Fatalf("events after ensure = %+v err=%v", events, err)
	}
	resetAt := now.Add(4 * time.Minute)
	if err := st.ResetDownStatusSince(t.Context(), id, resetAt); err != nil {
		t.Fatalf("ResetDownStatusSince: %v", err)
	}
	monitor, err := st.MonitorByID(t.Context(), id)
	if err != nil || monitor.State.StatusSince.UnixMilli() != resetAt.UnixMilli() {
		t.Fatalf("status since after reset = %+v err=%v", monitor.State, err)
	}
}

func TestUptimeAndRollupStatsForMonitors(t *testing.T) {
	st := newStoreTest(t)
	now := time.Now().UTC().Truncate(30 * time.Minute)
	first := createStoreHTTPMonitor(t, st, "First")
	second := createStoreHTTPMonitor(t, st, "Second")
	for _, id := range []int64{first, second} {
		if err := st.RecordCheck(t.Context(), model.Check{
			MonitorID: id, CheckedAt: now, Status: model.CheckUp, Latency: 10 * time.Millisecond, MetaJSON: "{}",
		}, model.MonitorState{
			Status: model.StatusUp, StatusSince: now, LastCheck: now, UpdatedAt: now,
		}, nil, false); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
	}
	stats, err := st.UptimeStatsForMonitors(t.Context(), []int64{first, second}, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("UptimeStatsForMonitors: %v", err)
	}
	if stats[first].Total != 1 || stats[second].Total != 1 {
		t.Fatalf("uptime stats = %+v", stats)
	}
	if err := st.AggregateHalfHourlyRollups(t.Context(), now, now.Add(30*time.Minute), now); err != nil {
		t.Fatalf("AggregateHalfHourlyRollups: %v", err)
	}
	rollup, err := st.RollupStatsForMonitors(t.Context(), []int64{first, second}, now, now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("RollupStatsForMonitors: %v", err)
	}
	if rollup[first].Total != 1 || rollup[second].Total != 1 {
		t.Fatalf("rollup stats = %+v", rollup)
	}
}

func TestUnreferencedCertificateAndDomainCleanup(t *testing.T) {
	st := newStoreTest(t)
	now := time.Now().UTC()
	if err := st.UpsertCertificate(t.Context(), model.CertificateInfo{
		Fingerprint: "cert-fp", Subject: "CN=example", Issuer: "CA", NotAfter: now.Add(30 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("UpsertCertificate: %v", err)
	}
	if err := st.UpsertDomainInfo(t.Context(), model.DomainInfo{
		Domain: "old.example", FetchedAt: now.Add(-100 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("UpsertDomainInfo old: %v", err)
	}
	if err := st.UpsertDomainInfo(t.Context(), model.DomainInfo{
		Domain: "fresh.example", FetchedAt: now,
	}); err != nil {
		t.Fatalf("UpsertDomainInfo fresh: %v", err)
	}
	if err := st.DeleteUnreferencedCertificates(t.Context(), now.Add(time.Hour)); err != nil {
		t.Fatalf("DeleteUnreferencedCertificates: %v", err)
	}
	var certCount int
	if err := st.db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM cert_info WHERE fingerprint = 'cert-fp'").Scan(&certCount); err != nil {
		t.Fatalf("count certificate: %v", err)
	}
	if certCount != 0 {
		t.Fatal("certificate was not cleaned up")
	}
	if err := st.DeleteDomainInfoBefore(t.Context(), now.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("DeleteDomainInfoBefore: %v", err)
	}
	if _, err := st.DomainInfo(t.Context(), "old.example"); err == nil {
		t.Fatal("old domain info was not cleaned up")
	}
	if _, err := st.DomainInfo(t.Context(), "fresh.example"); err != nil {
		t.Fatalf("fresh domain info: %v", err)
	}
}
