-- 0017_stream_state.down.sql
--
-- Roll back F-OBS-003 additive stream-state evidence columns.

BEGIN;

DROP INDEX IF EXISTS idx_billing_events_stream_state;
DROP INDEX IF EXISTS idx_usage_records_stream_state;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_delivered_token_count_check,
    DROP CONSTRAINT IF EXISTS billing_events_stream_state_check,
    DROP COLUMN IF EXISTS stream_terminated_reason,
    DROP COLUMN IF EXISTS delivered_token_count,
    DROP COLUMN IF EXISTS stream_state;

ALTER TABLE usage_records
    DROP CONSTRAINT IF EXISTS usage_records_delivered_token_count_check,
    DROP CONSTRAINT IF EXISTS usage_records_stream_state_check,
    DROP COLUMN IF EXISTS stream_terminated_reason,
    DROP COLUMN IF EXISTS delivered_token_count,
    DROP COLUMN IF EXISTS stream_state;

COMMIT;
