BEGIN;

CREATE TABLE IF NOT EXISTS cost_disputes (
    id BIGSERIAL PRIMARY KEY,
    dispute_id TEXT NOT NULL,
    tenant_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    request_id TEXT NOT NULL,
    reason TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 4000),
    status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'reviewing', 'resolved', 'rejected')),
    operator_note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    resolved_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT uq_cost_disputes_dispute_id UNIQUE (dispute_id),
    CONSTRAINT uq_cost_disputes_tenant_user_request UNIQUE (tenant_id, user_id, request_id),
    CONSTRAINT fk_cost_disputes_user
        FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_cost_disputes_tenant_status_created
    ON cost_disputes(tenant_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_cost_disputes_user_created
    ON cost_disputes(tenant_id, user_id, created_at DESC);

COMMENT ON TABLE cost_disputes IS
    'F-AUDIT-001 user-initiated cost dispute records. Pure status workflow; no refund or ledger mutation in this slice.';

COMMIT;
