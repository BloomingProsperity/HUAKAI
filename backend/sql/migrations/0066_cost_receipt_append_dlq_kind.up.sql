-- 0066 adds cost_receipt_append to usage_record_dlq.event_kind CHECK.
--
-- Tx2 may commit successfully while the post-settle receipt append
-- hook fails. The primary response must remain fail-open, but the missing
-- user_cost_receipts proof must be durable replay work instead of a log-only
-- warning.

BEGIN;
ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'audit_mismatch_refund', 'account_health', 'metrics',
             'audit_ledger_entry', 'post_delivery_settlement',
             'cost_receipt_append'));
COMMIT;
