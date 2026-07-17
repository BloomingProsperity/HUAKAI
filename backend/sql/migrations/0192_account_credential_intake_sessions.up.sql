BEGIN;

CREATE TABLE account_credential_intake_sessions (
    id                         uuid PRIMARY KEY,
    tenant_id                  bigint NOT NULL REFERENCES tenants(id),
    source_kind                text NOT NULL CHECK (source_kind IN ('claude_cookie')),
    vendor                     text NOT NULL CHECK (vendor = 'anthropic'),
    auth_mode                  text NOT NULL CHECK (auth_mode = 'claude_ai_oauth'),
    status                     text NOT NULL CHECK (status IN ('ready', 'consumed', 'expired', 'cancelled')),
    encrypted_candidate        bytea,
    encryption_scheme          text,
    encryption_key_id          text,
    encryption_nonce           bytea,
    encryption_aad_hash        text,
    candidate_commitment       text NOT NULL CHECK (candidate_commitment ~ '^[0-9a-f]{64}$'),
    redacted_context           jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(redacted_context) = 'object'),
    actor_id                   text NOT NULL,
    actor_role                 text NOT NULL CHECK (actor_role = 'tenant_operator'),
    request_id                 text,
    expires_at                 timestamptz NOT NULL,
    consumed_at                timestamptz,
    result_provider_account_id bigint,
    result_credential_id       bigint,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT account_credential_intake_session_state CHECK (
        (status = 'ready'
            AND encrypted_candidate IS NOT NULL
            AND encryption_scheme IS NOT NULL
            AND encryption_key_id IS NOT NULL
            AND encryption_nonce IS NOT NULL
            AND encryption_aad_hash IS NOT NULL
            AND consumed_at IS NULL
            AND result_provider_account_id IS NULL
            AND result_credential_id IS NULL)
        OR
        (status = 'consumed'
            AND encrypted_candidate IS NULL
            AND encryption_scheme IS NULL
            AND encryption_key_id IS NULL
            AND encryption_nonce IS NULL
            AND encryption_aad_hash IS NULL
            AND consumed_at IS NOT NULL
            AND result_provider_account_id IS NOT NULL
            AND result_credential_id IS NOT NULL)
        OR
        (status IN ('expired', 'cancelled')
            AND encrypted_candidate IS NULL
            AND encryption_scheme IS NULL
            AND encryption_key_id IS NULL
            AND encryption_nonce IS NULL
            AND encryption_aad_hash IS NULL
            AND consumed_at IS NULL
            AND result_provider_account_id IS NULL
            AND result_credential_id IS NULL)
    )
);

CREATE INDEX idx_account_credential_intake_ready_expiry
    ON account_credential_intake_sessions (expires_at, id)
    WHERE status = 'ready';

CREATE INDEX idx_account_credential_intake_tenant_recent
    ON account_credential_intake_sessions (tenant_id, created_at DESC, id DESC);

COMMENT ON TABLE account_credential_intake_sessions IS
    '账号创建前的短时凭据接入会话；原始 Cookie 禁止落库，转换后的候选凭据仅以认证加密形式短时保存。';
COMMENT ON COLUMN account_credential_intake_sessions.candidate_commitment IS
    '绑定预检与执行的一次性候选承诺，不包含可恢复的凭据材料。';

COMMIT;
