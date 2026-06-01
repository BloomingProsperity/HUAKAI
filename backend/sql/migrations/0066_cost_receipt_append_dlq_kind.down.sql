-- 0066 down: return usage_record_dlq.event_kind CHECK to the 0053 value set.
--
-- Refuse rollback while cost_receipt_append rows exist; otherwise rollback
-- would make durable receipt-recovery work illegal and strand settled money
-- without its user-facing proof snapshot.

BEGIN;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM usage_record_dlq WHERE event_kind = 'cost_receipt_append') THEN
        RAISE EXCEPTION 'cannot rollback 0066: cost_receipt_append DLQ rows exist; drain or quarantine them first';
    END IF;
END $$;

ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'audit_mismatch_refund', 'account_health', 'metrics',
             'audit_ledger_entry', 'post_delivery_settlement'));
COMMIT;
