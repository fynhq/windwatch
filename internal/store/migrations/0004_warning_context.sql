-- Persist the auxiliary warning context so a restart does not reset the warning
-- episode start or re-send certificate/domain warnings for an ongoing episode.
ALTER TABLE monitor_state ADD COLUMN warning_context TEXT NOT NULL DEFAULT '';
