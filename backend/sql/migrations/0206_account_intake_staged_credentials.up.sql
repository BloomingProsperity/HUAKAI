BEGIN;

CREATE TABLE account_intake_staged_credentials (
    id                  UUID PRIMARY KEY,
    tenant_id           BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_id            TEXT        NOT NULL CHECK (length(btrim(actor_id)) > 0),
    actor_role          TEXT        NOT NULL CHECK (actor_role = 'tenant_operator'),
    source_kind         TEXT        NOT NULL CHECK (source_kind IN ('claude_cookie', 'claude_setup_cookie', 'crs_sync')),
    vendor              TEXT        NOT NULL,
    auth_mode           TEXT        NOT NULL,
    plan_input          JSONB       NOT NULL CHECK (jsonb_typeof(plan_input) = 'object'),
    plan_hash           TEXT        NOT NULL CHECK (plan_hash ~ '^[0-9a-f]{64}$'),
    encrypted_content   BYTEA,
    encryption_scheme   TEXT,
    key_id              TEXT,
    nonce               BYTEA,
    aad_hash            TEXT,
    status              TEXT        NOT NULL DEFAULT 'staged'
        CHECK (status IN ('staged', 'claimed', 'completed', 'failed', 'expired')),
    expires_at          TIMESTAMPTZ NOT NULL,
    claimed_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_intake_staged_source_mode_check CHECK (
        (source_kind IN ('claude_cookie', 'claude_setup_cookie')
            AND vendor = 'anthropic' AND auth_mode IN ('claude_ai_oauth', 'claude_setup_token'))
        OR
        (source_kind = 'crs_sync' AND vendor IN ('anthropic', 'openai', 'gemini')
            AND auth_mode IN ('api_key', 'claude_ai_oauth', 'claude_setup_token',
                              'chatgpt_oauth', 'codex_cli_oauth', 'refresh_token',
                              'aistudio_api_key', 'code_assist', 'google_one'))
    ),
    CONSTRAINT account_intake_staged_secret_lifecycle CHECK (
        (status = 'staged' AND encrypted_content IS NOT NULL AND encryption_scheme IS NOT NULL
            AND key_id IS NOT NULL AND nonce IS NOT NULL AND aad_hash IS NOT NULL
            AND claimed_at IS NULL AND finished_at IS NULL)
        OR
        (status = 'claimed' AND encrypted_content IS NULL AND encryption_scheme IS NULL
            AND key_id IS NULL AND nonce IS NULL AND aad_hash IS NULL
            AND claimed_at IS NOT NULL AND finished_at IS NULL)
        OR
        (status IN ('completed', 'failed', 'expired') AND encrypted_content IS NULL
            AND encryption_scheme IS NULL AND key_id IS NULL AND nonce IS NULL AND aad_hash IS NULL)
    )
);

CREATE INDEX idx_account_intake_staged_expiry
    ON account_intake_staged_credentials (expires_at)
    WHERE status = 'staged';

CREATE INDEX idx_account_intake_staged_actor
    ON account_intake_staged_credentials (tenant_id, actor_id, created_at DESC);

COMMENT ON TABLE account_intake_staged_credentials IS
    '账号创建前的短期加密凭据暂存；Cookie 永不落库，领取时立即清除密文且不可重放。';

COMMIT;
