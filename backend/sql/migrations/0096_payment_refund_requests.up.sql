BEGIN;

CREATE TABLE IF NOT EXISTS payment_refund_requests (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   BIGINT      NOT NULL REFERENCES tenants(id),
    order_id    BIGINT      NOT NULL,
    user_id     BIGINT      NOT NULL,
    reason      TEXT,
    status      TEXT        NOT NULL
        CHECK (status IN ('pending', 'approved', 'rejected')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at  TIMESTAMPTZ,
    decided_by  BIGINT,
    CONSTRAINT uq_payment_refund_requests_order UNIQUE (tenant_id, order_id),
    CONSTRAINT uq_payment_refund_requests_tenant_id UNIQUE (tenant_id, id),
    CONSTRAINT fk_payment_refund_requests_order
        FOREIGN KEY (tenant_id, order_id) REFERENCES payment_orders (tenant_id, id),
    CONSTRAINT fk_payment_refund_requests_user
        FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_payment_refund_requests_pending
    ON payment_refund_requests (tenant_id, created_at, id)
    WHERE status = 'pending';

COMMIT;
