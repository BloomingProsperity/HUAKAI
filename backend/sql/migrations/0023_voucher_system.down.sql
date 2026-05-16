-- 0023_voucher_system.down.sql
--
-- DEV ROLLBACK ONLY. The billing_events CHECK keeps voucher_redeemed in the
-- allowed set so rollback cannot fail or invalidate already-written audit-grade
-- billing events. Voucher rows are dropped after removing FK edges.

BEGIN;

ALTER TABLE voucher_redemption
    DROP CONSTRAINT IF EXISTS fk_voucher_redemption_billing_event;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS fk_billing_events_voucher_redemption;

DROP INDEX IF EXISTS idx_billing_events_voucher_redemption;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_claim_or_voucher_check,
    ADD CONSTRAINT billing_events_claim_or_voucher_check
        CHECK (
            (event_type IN ('claim_committed', 'claim_aborted', 'reconciliation_appended')
                AND claim_id IS NOT NULL
                AND voucher_redemption_id IS NULL)
            OR
            (event_type = 'voucher_redeemed'
                AND voucher_redemption_id IS NOT NULL)
        );

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_event_type_check,
    ADD CONSTRAINT billing_events_event_type_check
        CHECK (event_type IN (
            'claim_committed',
            'claim_aborted',
            'reconciliation_appended',
            'voucher_redeemed'
        ));

DROP TABLE IF EXISTS voucher_burst_block;
DROP TABLE IF EXISTS voucher_redemption;
DROP TABLE IF EXISTS voucher;
DROP TABLE IF EXISTS voucher_batch;

COMMIT;
