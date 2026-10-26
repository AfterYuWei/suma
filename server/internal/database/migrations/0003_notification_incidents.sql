-- Notification evidence only. Existing history and queued snapshots are retained.
-- Historical events cannot establish whether a Docker fault is still active;
-- incidents therefore start from fresh observations after this upgrade.
ALTER TABLE notification_events ADD COLUMN incident_key varchar(64) NOT NULL DEFAULT '';
ALTER TABLE notification_events ADD COLUMN occurrences bigint NOT NULL DEFAULT 0;
ALTER TABLE notification_events ADD COLUMN last_occurred_at timestamptz;
ALTER TABLE notification_events ADD COLUMN resolved_at timestamptz;
CREATE UNIQUE INDEX idx_notification_events_active_incident
    ON notification_events (incident_key)
    WHERE incident_key <> '' AND resolved_at IS NULL;
