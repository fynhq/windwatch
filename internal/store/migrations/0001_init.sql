-- Consolidated schema baseline for the first release. Before the first release
-- the development migrations were squashed into this single initial migration.

CREATE TABLE admin (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'viewer')),
    disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    admin_id INTEGER NOT NULL REFERENCES admin(id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL
);

CREATE INDEX sessions_expires_at ON sessions (expires_at_ms);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE TABLE proxies (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('http', 'socks5')),
    address TEXT NOT NULL,
    username TEXT NOT NULL DEFAULT '',
    password_enc TEXT NOT NULL DEFAULT '',
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE UNIQUE INDEX proxies_name_unique ON proxies (name COLLATE NOCASE);

CREATE TABLE monitors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('http', 'tcp', 'docker', 'push')),
    config_json TEXT NOT NULL,
    interval_seconds INTEGER NOT NULL,
    timeout_seconds INTEGER NOT NULL,
    fail_threshold INTEGER NOT NULL,
    recover_threshold INTEGER NOT NULL,
    warning_reminder_count INTEGER NOT NULL DEFAULT 0,
    warning_reminder_interval_minutes INTEGER NOT NULL DEFAULT 1440,
    failure_reminder_count INTEGER NOT NULL DEFAULT 0,
    failure_reminder_interval_minutes INTEGER NOT NULL DEFAULT 30,
    notify_enabled INTEGER NOT NULL DEFAULT 1,
    enabled INTEGER NOT NULL DEFAULT 1,
    proxy_id INTEGER REFERENCES proxies(id) ON DELETE SET NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE INDEX monitors_proxy ON monitors (proxy_id);

CREATE TABLE monitor_state (
    monitor_id INTEGER PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'up', 'warning', 'down')),
    status_since_ms INTEGER NOT NULL,
    last_check_ms INTEGER,
    last_error TEXT NOT NULL DEFAULT '',
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    updated_at_ms INTEGER NOT NULL
);

CREATE TABLE checks (
    id INTEGER PRIMARY KEY,
    monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    checked_at_ms INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('up', 'warning', 'down')),
    latency_ms INTEGER NOT NULL,
    http_status INTEGER,
    error_kind TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    meta_json TEXT NOT NULL DEFAULT '{}',
    cert_fingerprint TEXT
);

CREATE INDEX checks_monitor_time ON checks (monitor_id, checked_at_ms DESC);
CREATE INDEX checks_time ON checks (checked_at_ms);

CREATE TABLE events (
    id INTEGER PRIMARY KEY,
    monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('down', 'up', 'warn_cert', 'warn_domain')),
    message TEXT NOT NULL DEFAULT '',
    started_at_ms INTEGER NOT NULL,
    ended_at_ms INTEGER,
    meta_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX events_monitor_time ON events (monitor_id, started_at_ms DESC);
CREATE INDEX events_time ON events (started_at_ms);
CREATE INDEX events_open_down ON events (monitor_id, type, ended_at_ms);

CREATE TABLE push_tokens (
    monitor_id INTEGER PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    token_enc TEXT NOT NULL,
    last_heartbeat_ms INTEGER,
    created_at_ms INTEGER NOT NULL
);

CREATE TABLE cert_info (
    fingerprint TEXT PRIMARY KEY,
    subject TEXT NOT NULL DEFAULT '',
    issuer TEXT NOT NULL DEFAULT '',
    sans_json TEXT NOT NULL DEFAULT '[]',
    not_before_ms INTEGER,
    not_after_ms INTEGER,
    updated_at_ms INTEGER NOT NULL
);

CREATE TABLE domain_info (
    domain TEXT PRIMARY KEY,
    registrar TEXT NOT NULL DEFAULT '',
    status_json TEXT NOT NULL DEFAULT '[]',
    expires_at_ms INTEGER,
    fetched_at_ms INTEGER NOT NULL,
    error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE notification_channels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    type TEXT NOT NULL CHECK (type IN ('telegram', 'webhook')),
    language TEXT NOT NULL DEFAULT 'zh-CN' CHECK (language IN ('zh-CN', 'en')),
    enabled INTEGER NOT NULL DEFAULT 1,
    telegram_chat_ids TEXT NOT NULL DEFAULT '',
    webhook_url TEXT NOT NULL DEFAULT '',
    secret_enc TEXT NOT NULL DEFAULT '',
    proxy_id INTEGER REFERENCES proxies(id) ON DELETE SET NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE TABLE monitor_notifications (
    monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    channel_id INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    PRIMARY KEY (monitor_id, channel_id)
);

CREATE INDEX monitor_notifications_channel ON monitor_notifications (channel_id);

CREATE TABLE notify_log (
    id INTEGER PRIMARY KEY,
    dedupe_key TEXT NOT NULL,
    channel TEXT NOT NULL,
    channel_id INTEGER NOT NULL DEFAULT 0,
    channel_name TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    error TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '',
    created_at_ms INTEGER NOT NULL,
    sent_at_ms INTEGER,
    UNIQUE (dedupe_key, channel_id, target)
);

CREATE INDEX notify_log_created ON notify_log (created_at_ms);
CREATE INDEX notify_log_status ON notify_log (status);

CREATE TABLE notification_reminders (
    monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('down', 'warning')),
    status_since_ms INTEGER NOT NULL,
    context_key TEXT NOT NULL DEFAULT '',
    sent_count INTEGER NOT NULL DEFAULT 0,
    next_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (monitor_id, kind)
);

CREATE TABLE rollups (
    monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    resolution_seconds INTEGER NOT NULL,
    bucket_start_ms INTEGER NOT NULL,
    total INTEGER NOT NULL,
    up INTEGER NOT NULL,
    warning INTEGER NOT NULL,
    down INTEGER NOT NULL,
    latency_sum_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (monitor_id, resolution_seconds, bucket_start_ms)
);

CREATE INDEX rollups_bucket ON rollups (resolution_seconds, bucket_start_ms);
