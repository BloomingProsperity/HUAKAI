-- HUAKAI alerting enrichment fields for rules, events, and silences.

BEGIN;

ALTER TABLE alert_rules
    ADD COLUMN IF NOT EXISTS metric_type TEXT NULL,
    ADD COLUMN IF NOT EXISTS sustained_seconds INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cooldown_seconds INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS notify_email BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS filters JSONB NULL,
    ADD COLUMN IF NOT EXISTS last_triggered_at TIMESTAMPTZ NULL;

ALTER TABLE alert_events
    ADD COLUMN IF NOT EXISTS threshold_value NUMERIC NULL,
    ADD COLUMN IF NOT EXISTS metric_value NUMERIC NULL,
    ADD COLUMN IF NOT EXISTS dimensions JSONB NULL,
    ADD COLUMN IF NOT EXISTS fired_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS email_sent BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE alert_events
    DROP CONSTRAINT IF EXISTS alert_events_state_check;

ALTER TABLE alert_events
    ADD CONSTRAINT alert_events_state_check
    CHECK (state IN ('firing', 'resolved', 'manual_resolved'));

ALTER TABLE alert_silences
    ADD COLUMN IF NOT EXISTS platform TEXT NULL,
    ADD COLUMN IF NOT EXISTS group_id TEXT NULL,
    ADD COLUMN IF NOT EXISTS region TEXT NULL;

COMMIT;
