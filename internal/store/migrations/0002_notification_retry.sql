ALTER TABLE notify_log ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notify_log ADD COLUMN next_attempt_at_ms INTEGER;

CREATE INDEX notify_log_pending_due ON notify_log (status, next_attempt_at_ms, created_at_ms);
