BEGIN;

CREATE TABLE IF NOT EXISTS payment_refunds (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       BIGINT      NOT NULL REFERENCES tenants(id),
    order_id        BIGINT      NOT NULL,
    user_id         BIGINT      NOT NULL,
    amount_cents    BIGINT      NOT NULL CHECK (amount_cents > 0),
    currency        TEXT        NOT NULL,
    idempotency_key TEXT        NOT NULL,
    reason          TEXT,
    actor_kind      TEXT        NOT NULL,
    actor_id        BIGINT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_payment_refunds_order UNIQUE (tenant_id, order_id),
    CONSTRAINT uq_payment_refunds_idempotency_key UNIQUE (tenant_id, idempotency_key),
    CONSTRAINT fk_payment_refunds_order
        FOREIGN KEY (tenant_id, order_id) REFERENCES payment_orders (tenant_id, id),
    CONSTRAINT fk_payment_refunds_user
        FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_payment_refunds_tenant_id
    ON payment_refunds (tenant_id, id);
CREATE INDEX IF NOT EXISTS idx_payment_refunds_user_time
    ON payment_refunds (tenant_id, user_id, created_at DESC);

ALTER TABLE payment_orders DROP CONSTRAINT IF EXISTS payment_orders_status_check;
ALTER TABLE payment_orders ADD CONSTRAINT payment_orders_status_check
    CHECK (status IN ('pending', 'paid', 'recharging', 'completed', 'refunded', 'expired', 'cancelled', 'failed'));

ALTER TABLE payment_audit_events DROP CONSTRAINT IF EXISTS payment_audit_events_event_type_check;
ALTER TABLE payment_audit_events ADD CONSTRAINT payment_audit_events_event_type_check
    CHECK (event_type IN (
        'order_created', 'paid_confirmed', 'fulfillment_started', 'credited',
        'fulfillment_failed', 'idempotent_replay', 'order_expired', 'order_cancelled',
        'order_refunded'
    ));

ALTER TABLE billing_events
    ADD COLUMN IF NOT EXISTS payment_refund_id BIGINT;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_event_type_check,
    ADD CONSTRAINT billing_events_event_type_check
        CHECK (event_type IN (
            'claim_committed',
            'claim_aborted',
            'reconciliation_appended',
            'voucher_redeemed',
            'balance_recharged',
            'payment_credited',
            'payment_refunded'
        ));

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_claim_or_voucher_check,
    ADD CONSTRAINT billing_events_claim_or_voucher_check
        CHECK (
            (event_type IN ('claim_committed', 'claim_aborted', 'reconciliation_appended')
                AND claim_id IS NOT NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NULL)
            OR
            (event_type = 'voucher_redeemed'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NOT NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NULL)
            OR
            (event_type = 'balance_recharged'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NOT NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NULL)
            OR
            (event_type = 'payment_credited'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NOT NULL
                AND payment_refund_id IS NULL)
            OR
            (event_type = 'payment_refunded'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NOT NULL)
        );

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS fk_billing_events_payment_refund,
    ADD CONSTRAINT fk_billing_events_payment_refund
        FOREIGN KEY (tenant_id, payment_refund_id)
        REFERENCES payment_refunds (tenant_id, id);

CREATE INDEX IF NOT EXISTS idx_billing_events_payment_refund
    ON billing_events (tenant_id, payment_refund_id)
    WHERE payment_refund_id IS NOT NULL;

COMMIT;
