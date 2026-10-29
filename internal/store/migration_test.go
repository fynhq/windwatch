package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestMigrateWebhookFormatUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	script, err := migrationFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if _, err := db.ExecContext(ctx, string(script)); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at_ms INTEGER NOT NULL)"); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at_ms) VALUES (1, 1)"); err != nil {
		t.Fatalf("record baseline: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO notification_channels (
			name, type, language, enabled, telegram_chat_ids, webhook_url, secret_enc,
			proxy_id, created_at_ms, updated_at_ms
		) VALUES ('Legacy hook', 'webhook', 'zh-CN', 1, '', 'https://example.com/hook', '', NULL, 1, 1)
	`); err != nil {
		t.Fatalf("insert legacy channel: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close upgrade database: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open upgraded database: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate upgraded database: %v", err)
	}
	channel, err := st.NotificationChannelByID(ctx, 1)
	if err != nil {
		t.Fatalf("NotificationChannelByID: %v", err)
	}
	if channel.WebhookFormat != model.WebhookFormatGeneric {
		t.Fatalf("legacy webhook format = %q, want generic", channel.WebhookFormat)
	}
	var versions int
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&versions); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if versions != 2 {
		t.Fatalf("schema versions after upgrade = %d, want 2", versions)
	}
}

func TestMigrateFreshDatabase(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate twice: %v", err)
	}

	var versions int
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&versions); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if versions != 2 {
		t.Fatalf("schema versions = %d, want 2", versions)
	}

	expectedTables := []string{
		"admin", "sessions", "settings", "proxies", "monitors", "monitor_state",
		"checks", "events", "push_tokens", "cert_info", "domain_info",
		"notification_channels", "monitor_notifications", "notify_log",
		"notification_reminders", "rollups",
	}
	for _, table := range expectedTables {
		var count int
		if err := st.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("missing table %s", table)
		}
	}

	var reminderColumns int
	if err := st.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pragma_table_info('monitors')
		WHERE name IN (
			'warning_reminder_count', 'warning_reminder_interval_minutes',
			'failure_reminder_count', 'failure_reminder_interval_minutes'
		)
	`).Scan(&reminderColumns); err != nil {
		t.Fatalf("check reminder columns: %v", err)
	}
	if reminderColumns != 4 {
		t.Fatalf("reminder columns = %d, want 4", reminderColumns)
	}

	now := time.Now().UTC()
	firstProxy, err := st.CreateProxy(ctx, model.Proxy{
		Name: "Office", Type: model.ProxyHTTP, Address: "127.0.0.1:7890",
	}, now)
	if err != nil {
		t.Fatalf("CreateProxy: %v", err)
	}
	if _, err := st.CreateProxy(ctx, model.Proxy{
		Name: "office", Type: model.ProxyHTTP, Address: "127.0.0.1:7891",
	}, now); !errors.Is(err, ErrProxyNameTaken) {
		t.Fatalf("duplicate proxy error = %v, want ErrProxyNameTaken", err)
	}

	channelID, err := st.CreateNotificationChannel(ctx, model.NotificationChannel{
		Name: "Ops hook", Type: model.NotificationChannelWebhook, Enabled: true,
		WebhookURL: "https://example.com/hook",
	}, now)
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	channel, err := st.NotificationChannelByID(ctx, channelID)
	if err != nil || channel.Language != model.NotificationLanguageZH {
		t.Fatalf("channel language = %q err=%v", channel.Language, err)
	}
	if channel.ResolvedWebhookFormat() != model.WebhookFormatGeneric {
		t.Fatalf("default webhook format = %q", channel.WebhookFormat)
	}
	if _, err := st.CreateNotificationChannel(ctx, model.NotificationChannel{
		Name: "ops hook", Type: model.NotificationChannelWebhook, Enabled: true,
		WebhookURL: "https://example.com/other",
	}, now); !errors.Is(err, ErrNotificationChannelNameTaken) {
		t.Fatalf("duplicate channel error = %v, want ErrNotificationChannelNameTaken", err)
	}
	if _, err := st.CreateNotificationChannel(ctx, model.NotificationChannel{
		Name: "invalid hook", Type: model.NotificationChannelWebhook, Enabled: true,
		WebhookURL: "https://example.com/invalid", WebhookFormat: "slack",
	}, now); !errors.Is(err, model.ErrChannelWebhookFormat) {
		t.Fatalf("invalid webhook format error = %v, want ErrChannelWebhookFormat", err)
	}
	dingID, err := st.CreateNotificationChannel(ctx, model.NotificationChannel{
		Name: "DingTalk hook", Type: model.NotificationChannelWebhook, Enabled: true,
		WebhookURL: "https://example.com/ding", WebhookFormat: model.WebhookFormatDingTalk,
	}, now)
	if err != nil {
		t.Fatalf("CreateNotificationChannel with format: %v", err)
	}
	dingChannel, err := st.NotificationChannelByID(ctx, dingID)
	if err != nil || dingChannel.WebhookFormat != model.WebhookFormatDingTalk {
		t.Fatalf("webhook format round trip = %q err=%v", dingChannel.WebhookFormat, err)
	}

	monitorID, err := st.CreateMonitor(ctx, model.Monitor{
		Name: "Example", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1, Enabled: true, NotifyEnabled: true,
		ProxyID:                &firstProxy,
		NotificationChannelIDs: []int64{channelID},
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	monitor, err := st.MonitorByID(ctx, monitorID)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if monitor.ProxyID == nil || *monitor.ProxyID != firstProxy ||
		len(monitor.NotificationChannelIDs) != 1 || monitor.NotificationChannelIDs[0] != channelID {
		t.Fatalf("monitor relations = %+v", monitor)
	}

	if err := st.DeleteNotificationChannel(ctx, channelID); err != nil {
		t.Fatalf("DeleteNotificationChannel: %v", err)
	}
	monitor, err = st.MonitorByID(ctx, monitorID)
	if err != nil {
		t.Fatalf("MonitorByID after channel delete: %v", err)
	}
	if len(monitor.NotificationChannelIDs) != 0 {
		t.Fatalf("monitor channel links survived delete: %v", monitor.NotificationChannelIDs)
	}

	if err := st.DeleteProxy(ctx, firstProxy); err != nil {
		t.Fatalf("DeleteProxy: %v", err)
	}
	monitor, err = st.MonitorByID(ctx, monitorID)
	if err != nil {
		t.Fatalf("MonitorByID after proxy delete: %v", err)
	}
	if monitor.ProxyID != nil {
		t.Fatalf("monitor proxy reference survived delete: %v", monitor.ProxyID)
	}

	var foreignKeys int
	if err := st.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatal("foreign keys are not enabled")
	}
}
