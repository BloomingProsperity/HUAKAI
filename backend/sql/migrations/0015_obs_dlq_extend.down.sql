-- 0015_obs_dlq_extend.down.sql
--
-- Conservative rollback for F-OBS-005 DLQ extension. If generic rows with
-- NULL claim_id exist, rollback stops instead of silently deleting evidence.

BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM usage_record_dlq WHERE claim_id IS NULL) THEN
        RAISE EXCEPTION 'cannot rollback 0015_obs_dlq_extend: usage_record_dlq has generic rows with NULL claim_id';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_usage_dlq_replica_status;
DROP INDEX IF EXISTS idx_usage_dlq_operator_review;
DROP INDEX IF EXISTS idx_usage_dlq_lease_expired;
DROP INDEX IF EXISTS idx_usage_dlq_claim_due;
DROP INDEX IF EXISTS uq_usage_dlq_idempotency;

ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_replica_status_check,
    DROP CONSTRAINT IF EXISTS usage_record_dlq_status_check,
    DROP CONSTRAINT IF EXISTS usage_record_dlq_lane_check,
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check;

ALTER TABLE usage_record_dlq
    ALTER COLUMN claim_id SET NOT NULL,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS operator_review_at,
    DROP COLUMN IF EXISTS source_id,
    DROP COLUMN IF EXISTS source_table,
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS replica_committed_at,
    DROP COLUMN IF EXISTS replica_target,
    DROP COLUMN IF EXISTS replica_status,
    DROP COLUMN IF EXISTS lease_until,
    DROP COLUMN IF EXISTS lease_owner,
    DROP COLUMN IF EXISTS lease_ttl,
    DROP COLUMN IF EXISTS next_retry_at,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS lane,
    DROP COLUMN IF EXISTS event_kind;

COMMENT ON TABLE usage_record_dlq IS
    'F-OBS-001 H10: durable DLQ for Usage Record write failures. Operator-replayable; auto-replay cadence configurable.';

COMMIT;
