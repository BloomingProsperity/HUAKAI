-- 0063_billing_events_balance_recharged.up.sql
--
-- MONEY-4: extend billing_events so successful recharge fulfillment appears in
-- the same append-only money event stream as claim and voucher events.

BEGIN;

ALTER TABLE billing_events
    ADD COLUMN IF NOT EXISTS recharge_order_id bigint;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_event_type_check,
    ADD CONSTRAINT billing_events_event_type_check
        CHECK (event_type IN (
            'claim_committed',
            'claim_aborted',
            'reconciliation_appended',
            'voucher_redeemed',
            'balance_recharged'
        ));

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_claim_or_voucher_check,
    ADD CONSTRAINT billing_events_claim_or_voucher_check
        CHECK (
            (event_type IN ('claim_committed', 'claim_aborted', 'reconciliation_appended')
                AND claim_id IS NOT NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL)
            OR
            (event_type = 'voucher_redeemed'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NOT NULL
                AND recharge_order_id IS NULL)
            OR
            (event_type = 'balance_recharged'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NOT NULL)
        );

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS fk_billing_events_recharge_order,
    ADD CONSTRAINT fk_billing_events_recharge_order
        FOREIGN KEY (tenant_id, recharge_order_id)
        REFERENCES recharge_orders (tenant_id, id);

CREATE INDEX IF NOT EXISTS idx_billing_events_recharge_order
    ON billing_events (tenant_id, recharge_order_id)
    WHERE recharge_order_id IS NOT NULL;

COMMENT ON COLUMN billing_events.recharge_order_id IS
    'MONEY-4 successful recharge event link. Non-null only when event_type=balance_recharged.';

COMMIT;
