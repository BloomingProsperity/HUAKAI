BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM usage_record_dlq
        WHERE event_kind = 'audit_mismatch_refund'
    ) THEN
        RAISE EXCEPTION 'cannot rollback 0032_audit_mismatch_refund_pending: audit_mismatch_refund DLQ rows exist';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_audit_refund_pending_status;
DROP TABLE IF EXISTS audit_refund_pending;

ALTER TABLE user_cost_receipts
    DROP CONSTRAINT IF EXISTS user_cost_receipts_adjustment_refs_array_check,
    DROP CONSTRAINT IF EXISTS user_cost_receipts_verdict_check,
    DROP CONSTRAINT IF EXISTS user_cost_receipts_validation_state_check,
    DROP COLUMN IF EXISTS adjustment_refs,
    DROP COLUMN IF EXISTS verdict,
    DROP COLUMN IF EXISTS validation_state;

ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'account_health', 'metrics'));

COMMIT;
