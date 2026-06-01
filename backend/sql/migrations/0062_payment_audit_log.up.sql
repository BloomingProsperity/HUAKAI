-- 0062_payment_audit_log.up.sql
--
-- MONEY-4: redacted payment callback audit. This table records callback
-- outcomes and anti-tamper reasons without storing raw webhook bodies or
-- signatures.

BEGIN;

CREATE TABLE IF NOT EXISTS payment_audit_log (
    id                  bigserial PRIMARY KEY,
    tenant_id           bigint        NOT NULL REFERENCES tenants(id),
    recharge_order_id   bigint,
    user_id             bigint,
    provider            text          NOT NULL,
    external_trade_no   text          NOT NULL,
    provider_event_id   text          NOT NULL DEFAULT '',
    outcome             text          NOT NULL
        CHECK (outcome IN ('ACCEPTED','REJECTED','REPLAY_NOOP')),
    reason              text          NOT NULL
        CHECK (reason IN (
            'PAYMENT_COMPLETED',
            'PAYMENT_REPLAY',
            'PAYMENT_AMOUNT_MISMATCH',
            'PAYMENT_PROVIDER_MISMATCH',
            'PAYMENT_ORDER_NOT_FOUND',
            'PAYMENT_ORDER_STATE_MISMATCH'
        )),
    paid_amount         numeric(20,8),
    expected_amount     numeric(20,8),
    currency_code       char(3)       NOT NULL DEFAULT 'USD',
    metadata            jsonb         NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, recharge_order_id) REFERENCES recharge_orders (tenant_id, id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX IF NOT EXISTS idx_payment_audit_log_order
    ON payment_audit_log (tenant_id, recharge_order_id, created_at DESC)
    WHERE recharge_order_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_payment_audit_log_trade
    ON payment_audit_log (tenant_id, provider, external_trade_no, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_payment_audit_log_reason
    ON payment_audit_log (tenant_id, reason, created_at DESC);

COMMENT ON TABLE payment_audit_log IS
    'MONEY-4 redacted payment callback audit. Raw webhook payloads and signatures are intentionally not stored.';
COMMENT ON COLUMN payment_audit_log.provider_event_id IS
    'Provider callback event identifier for support correlation; not trusted for balance idempotency.';

COMMIT;
