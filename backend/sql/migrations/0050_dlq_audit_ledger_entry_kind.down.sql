BEGIN;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM usage_record_dlq WHERE event_kind = 'audit_ledger_entry') THEN
        RAISE EXCEPTION 'cannot rollback 0050: audit_ledger_entry DLQ rows exist';
    END IF;
END $$;
ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'audit_mismatch_refund', 'account_health', 'metrics'));
COMMIT;
