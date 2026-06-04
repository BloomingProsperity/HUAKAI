BEGIN;

CREATE TABLE IF NOT EXISTS pricing_ratio_audit_log (
    id            bigserial     PRIMARY KEY,
    occurred_at   timestamptz   NOT NULL,
    actor_id      text          NOT NULL,
    actor_role    text          NOT NULL,
    tenant_id     bigint        NOT NULL,
    pool_group_id bigint        NOT NULL,
    action        text          NOT NULL CHECK (action IN ('upsert', 'delete')),
    old_ratio     numeric(20,8),
    new_ratio     numeric(20,8),
    prev_hash     bytea,
    entry_hash    bytea         NOT NULL CHECK (octet_length(entry_hash) = 32),
    signature     bytea         NOT NULL CHECK (octet_length(signature) = 64),
    key_id        text          NOT NULL,
    CONSTRAINT pricing_ratio_audit_log_prev_hash_len
        CHECK (prev_hash IS NULL OR octet_length(prev_hash) = 32),
    CONSTRAINT pricing_ratio_audit_log_ratio_shape
        CHECK (
            (action = 'upsert' AND new_ratio IS NOT NULL)
            OR
            (action = 'delete' AND old_ratio IS NOT NULL AND new_ratio IS NULL)
        )
);

CREATE INDEX IF NOT EXISTS idx_pricing_ratio_audit_scope_time
    ON pricing_ratio_audit_log (tenant_id, pool_group_id, occurred_at);

COMMENT ON TABLE pricing_ratio_audit_log IS
    'Append-only signed hash-chain audit log for pool_group_pricing_ratios changes.';

COMMIT;
