-- 0017_stream_state.up.sql
--
-- F-OBS-003: four-state failed-stream billing evidence.
-- stream_state:
--   0 = Acquired, 1 = InFlight, 2 = Partial, 3 = Failed
--
-- Additive only. Existing settled rows are backfilled as Partial because they
-- already represent delivered/settled work in the current ledger model.

BEGIN;

ALTER TABLE usage_records
    ADD COLUMN IF NOT EXISTS stream_state smallint NOT NULL DEFAULT 2,
    ADD COLUMN IF NOT EXISTS delivered_token_count bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS stream_terminated_reason varchar(64);

ALTER TABLE billing_events
    ADD COLUMN IF NOT EXISTS stream_state smallint NOT NULL DEFAULT 2,
    ADD COLUMN IF NOT EXISTS delivered_token_count bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS stream_terminated_reason varchar(64);

ALTER TABLE usage_records
    DROP CONSTRAINT IF EXISTS usage_records_stream_state_check,
    ADD CONSTRAINT usage_records_stream_state_check
        CHECK (stream_state IN (0, 1, 2, 3)),
    DROP CONSTRAINT IF EXISTS usage_records_delivered_token_count_check,
    ADD CONSTRAINT usage_records_delivered_token_count_check
        CHECK (delivered_token_count >= 0);

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_stream_state_check,
    ADD CONSTRAINT billing_events_stream_state_check
        CHECK (stream_state IN (0, 1, 2, 3)),
    DROP CONSTRAINT IF EXISTS billing_events_delivered_token_count_check,
    ADD CONSTRAINT billing_events_delivered_token_count_check
        CHECK (delivered_token_count >= 0);

CREATE INDEX IF NOT EXISTS idx_usage_records_stream_state
    ON usage_records (tenant_id, stream_state, settled_at DESC);

CREATE INDEX IF NOT EXISTS idx_billing_events_stream_state
    ON billing_events (tenant_id, stream_state, occurred_at DESC);

COMMENT ON COLUMN usage_records.stream_state IS
    'F-OBS-003 stream billing state: 0=Acquired, 1=InFlight, 2=Partial/delivered, 3=Failed/no-charge.';
COMMENT ON COLUMN usage_records.delivered_token_count IS
    'F-OBS-003 client-visible delivered output token count; falls back to delivered chunk count when upstream usage is absent.';
COMMENT ON COLUMN usage_records.stream_terminated_reason IS
    'F-OBS-003 first-class stream terminal reason, e.g. client_gone, upstream_timeout, output_token_zero, upstream_5xx.';

COMMENT ON COLUMN billing_events.stream_state IS
    'F-OBS-003 stream billing state copied from the paired usage attempt for audit fallback.';
COMMENT ON COLUMN billing_events.delivered_token_count IS
    'F-OBS-003 delivered output token count copied into billing event audit trail.';
COMMENT ON COLUMN billing_events.stream_terminated_reason IS
    'F-OBS-003 first-class stream terminal reason copied into billing event audit trail.';

COMMIT;
