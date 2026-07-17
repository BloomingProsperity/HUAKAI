BEGIN;

CREATE TABLE account_source_intake_sessions (
    id                     uuid PRIMARY KEY,
    tenant_id              bigint NOT NULL REFERENCES tenants(id),
    source_kind            text NOT NULL CHECK (source_kind IN ('crs_sync', 'account_bundle_recovery')),
    status                 text NOT NULL CHECK (status IN ('ready', 'expired', 'cancelled')),
    encrypted_items        bytea,
    encryption_scheme      text,
    encryption_key_id      text,
    encryption_nonce       bytea,
    encryption_aad_hash    text,
    source_commitment      text NOT NULL CHECK (source_commitment ~ '^[0-9a-f]{64}$'),
    item_count             integer NOT NULL CHECK (item_count > 0 AND item_count <= 500),
    redacted_context       jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(redacted_context) = 'object'),
    actor_id               text NOT NULL,
    actor_role             text NOT NULL CHECK (actor_role = 'tenant_operator'),
    request_id             text,
    expires_at             timestamptz NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT account_source_intake_session_state CHECK (
        (status = 'ready'
            AND encrypted_items IS NOT NULL
            AND encryption_scheme IS NOT NULL
            AND encryption_key_id IS NOT NULL
            AND encryption_nonce IS NOT NULL
            AND encryption_aad_hash IS NOT NULL)
        OR
        (status IN ('expired', 'cancelled')
            AND encrypted_items IS NULL
            AND encryption_scheme IS NULL
            AND encryption_key_id IS NULL
            AND encryption_nonce IS NULL
            AND encryption_aad_hash IS NULL)
    )
);

CREATE INDEX idx_account_source_intake_ready_expiry
    ON account_source_intake_sessions (expires_at, id)
    WHERE status = 'ready';

CREATE INDEX idx_account_source_intake_tenant_recent
    ON account_source_intake_sessions (tenant_id, created_at DESC, id DESC);

COMMENT ON TABLE account_source_intake_sessions IS
    '远程账号源和加密恢复包解析后的短时账号候选；管理密码和恢复口令禁止持久化。';
COMMENT ON COLUMN account_source_intake_sessions.source_commitment IS
    '绑定短时密文批次与逐项预检计划的单向承诺。';

COMMIT;
