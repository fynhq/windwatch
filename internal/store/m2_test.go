package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestProxyCRUD(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	proxy := model.Proxy{
		Name:        "Clash",
		Type:        model.ProxyHTTP,
		Address:     "127.0.0.1:7890",
		Username:    "user",
		PasswordEnc: "encrypted",
	}
	id, err := st.CreateProxy(ctx, proxy, now)
	if err != nil {
		t.Fatalf("CreateProxy: %v", err)
	}
	stored, err := st.ProxyByID(ctx, id)
	if err != nil {
		t.Fatalf("ProxyByID: %v", err)
	}
	if stored.Name != "Clash" || stored.PasswordEnc != "encrypted" {
		t.Fatalf("unexpected proxy: %+v", stored)
	}
	stored.Address = "127.0.0.1:7891"
	if err := st.UpdateProxy(ctx, stored, now.Add(time.Minute)); err != nil {
		t.Fatalf("UpdateProxy: %v", err)
	}
	updated, err := st.ProxyByID(ctx, id)
	if err != nil || updated.Address != "127.0.0.1:7891" {
		t.Fatalf("updated proxy=%+v err=%v", updated, err)
	}
	if err := st.DeleteProxy(ctx, id); err != nil {
		t.Fatalf("DeleteProxy: %v", err)
	}
	if _, err := st.ProxyByID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted proxy error = %v", err)
	}
}

func TestPushTokenAndHeartbeat(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	monitor := model.Monitor{
		Name:             "Backup",
		Type:             model.MonitorTypePush,
		Interval:         5 * time.Minute,
		Timeout:          time.Minute,
		FailThreshold:    2,
		RecoverThreshold: 1,
		Enabled:          true,
	}
	id, err := st.CreateMonitor(ctx, monitor, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if err := st.CreatePushToken(ctx, id, "token-hash", "token-enc", now); err != nil {
		t.Fatalf("CreatePushToken: %v", err)
	}
	token, err := st.PushTokenByHash(ctx, "token-hash")
	if err != nil || token.MonitorID != id || !token.LastHeartbeat.IsZero() {
		t.Fatalf("PushTokenByHash=%+v err=%v", token, err)
	}
	heartbeatAt := now.Add(10 * time.Minute)
	if err := st.RecordHeartbeat(ctx, id, heartbeatAt); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	token, err = st.PushTokenByMonitorID(ctx, id)
	if err != nil || !token.LastHeartbeat.Equal(heartbeatAt) {
		t.Fatalf("after heartbeat token=%+v err=%v", token, err)
	}
	if err := st.DeleteMonitor(ctx, id); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	if _, err := st.PushTokenByMonitorID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("push token was not cascaded: %v", err)
	}
}

func TestCertificateAndDomainInfo(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	id, err := st.CreateMonitor(ctx, testHTTPMonitor("https://example.com"), now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	cert := model.CertificateInfo{
		Fingerprint: "fp",
		Subject:     "CN=example.com",
		Issuer:      "CN=Test CA",
		SANs:        []string{"example.com"},
		NotBefore:   now,
		NotAfter:    now.Add(90 * 24 * time.Hour),
	}
	if err := st.UpsertCertificate(ctx, cert); err != nil {
		t.Fatalf("UpsertCertificate: %v", err)
	}
	if err := st.RecordCheck(ctx, model.Check{
		MonitorID:       id,
		CheckedAt:       now,
		Status:          model.CheckUp,
		Latency:         time.Millisecond,
		MetaJSON:        "{}",
		CertFingerprint: "fp",
	}, model.MonitorState{
		Status:      model.StatusUp,
		StatusSince: now,
		LastCheck:   now,
		UpdatedAt:   now,
	}, nil, false); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	latest, err := st.LatestCertificate(ctx, id)
	if err != nil || latest.Fingerprint != "fp" || latest.NotAfter.IsZero() {
		t.Fatalf("LatestCertificate=%+v err=%v", latest, err)
	}

	info := model.DomainInfo{
		Domain:    "example.com",
		Registrar: "Example Registrar",
		Statuses:  []string{"clientTransferProhibited"},
		ExpiresAt: now.Add(365 * 24 * time.Hour),
		FetchedAt: now,
	}
	if err := st.UpsertDomainInfo(ctx, info); err != nil {
		t.Fatalf("UpsertDomainInfo: %v", err)
	}
	stored, err := st.DomainInfo(ctx, "example.com")
	if err != nil || stored.Registrar != info.Registrar || len(stored.Statuses) != 1 {
		t.Fatalf("DomainInfo=%+v err=%v", stored, err)
	}
}
