-- Roll back HUAKAI alerting enrichment fields.

BEGIN;

ALTER TABLE alert_silences
    DROP COLUMN IF EXISTS region,
    DROP COLUMN IF EXISTS group_id,
    DROP COLUMN IF EXISTS platform;

UPDATE alert_events
SET state = 'resolved'
WHERE state = 'manual_resolved';

ALTER TABLE alert_events
    DROP CONSTRAINT IF EXISTS alert_events_state_check;

ALTER TABLE alert_events
    ADD CONSTRAINT alert_events_state_check
    CHECK (state IN ('firing', 'resolved'));

ALTER TABLE alert_events
    DROP COLUMN IF EXISTS email_sent,
    DROP COLUMN IF EXISTS dimensions,
    DROP COLUMN IF EXISTS metric_value,
    DROP COLUMN IF EXISTS threshold_value;

ALTER TABLE alert_rules
    DROP COLUMN IF EXISTS last_triggered_at,
    DROP COLUMN IF EXISTS filters,
    DROP COLUMN IF EXISTS notify_email,
    DROP COLUMN IF EXISTS cooldown_seconds,
    DROP COLUMN IF EXISTS sustained_seconds,
    DROP COLUMN IF EXISTS metric_type;

COMMIT;
