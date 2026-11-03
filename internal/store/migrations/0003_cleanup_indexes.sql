-- Indexes used by maintenance cleanup. The events and certificate tables can
-- grow independently of the seven-day raw check retention.
CREATE INDEX IF NOT EXISTS events_ended_at ON events (ended_at_ms);
CREATE INDEX IF NOT EXISTS checks_cert_fingerprint ON checks (cert_fingerprint);
