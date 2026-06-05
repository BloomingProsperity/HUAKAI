BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_refunds)
       OR EXISTS (SELECT 1 FROM billing_events WHERE event_type = 'payment_refunded')
       OR EXISTS (SELECT 1 FROM payment_orders WHERE status = 'refunded') THEN
        RAISE EXCEPTION 'refusing to roll back 0092: payment refund money data exists; production rollback must go through an Owner-gated data plan';
    END IF;
END $$;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS fk_billing_events_payment_refund;
DROP INDEX IF EXISTS idx_billing_events_payment_refund;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_claim_or_voucher_check;
ALTER TABLE billing_events
    DROP COLUMN IF EXISTS payment_refund_id;
ALTER TABLE billing_events
    ADD CONSTRAINT billing_events_claim_or_voucher_check
        CHECK (
            (event_type IN ('claim_committed', 'claim_aborted', 'reconciliation_appended')
                AND claim_id IS NOT NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL)
            OR
            (event_type = 'voucher_redeemed'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NOT NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL)
            OR
            (event_type = 'balance_recharged'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NOT NULL
                AND payment_credit_id IS NULL)
            OR
            (event_type = 'payment_credited'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NOT NULL)
        );

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_event_type_check,
    ADD CONSTRAINT billing_events_event_type_check
        CHECK (event_type IN (
            'claim_committed',
            'claim_aborted',
            'reconciliation_appended',
            'voucher_redeemed',
            'balance_recharged',
            'payment_credited'
        ));

ALTER TABLE payment_audit_events DROP CONSTRAINT IF EXISTS payment_audit_events_event_type_check;
ALTER TABLE payment_audit_events ADD CONSTRAINT payment_audit_events_event_type_check
    CHECK (event_type IN (
        'order_created', 'paid_confirmed', 'fulfillment_started', 'credited',
        'fulfillment_failed', 'idempotent_replay', 'order_expired', 'order_cancelled'
    ));

ALTER TABLE payment_orders DROP CONSTRAINT IF EXISTS payment_orders_status_check;
ALTER TABLE payment_orders ADD CONSTRAINT payment_orders_status_check
    CHECK (status IN ('pending', 'paid', 'recharging', 'completed', 'expired', 'cancelled', 'failed'));

DROP TABLE IF EXISTS payment_refunds;

COMMIT;
