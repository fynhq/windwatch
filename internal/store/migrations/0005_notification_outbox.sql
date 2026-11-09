CREATE TABLE notification_outbox (
    id INTEGER PRIMARY KEY,
    dedupe_key TEXT NOT NULL DEFAULT '',
    monitor_id INTEGER NOT NULL DEFAULT 0,
    payload_json TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at_ms INTEGER,
    last_error TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX notification_outbox_dedupe
    ON notification_outbox (dedupe_key)
    WHERE dedupe_key <> '';

CREATE INDEX notification_outbox_due
    ON notification_outbox (next_attempt_at_ms, created_at_ms);
