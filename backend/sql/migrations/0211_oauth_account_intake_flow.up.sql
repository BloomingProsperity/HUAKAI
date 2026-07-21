BEGIN;

-- 账号创建型 OAuth 在授权前还没有 provider_account。只放宽短期获取会话的绑定列，
-- 真实凭据仍由 account_credentials 强制绑定已存在账号。
ALTER TABLE credential_acquisition_flow_sessions
    ALTER COLUMN provider_account_id DROP NOT NULL;

ALTER TABLE account_intake_staged_credentials
    DROP CONSTRAINT account_intake_staged_credentials_actor_role_check,
    DROP CONSTRAINT account_intake_staged_credentials_source_kind_check,
    DROP CONSTRAINT account_intake_staged_credentials_status_check,
    DROP CONSTRAINT account_intake_staged_source_mode_check,
    DROP CONSTRAINT account_intake_staged_secret_lifecycle;

ALTER TABLE account_intake_staged_credentials
    ADD CONSTRAINT account_intake_staged_credentials_actor_role_check
        CHECK (actor_role IN ('platform_admin', 'tenant_operator')),
    ADD CONSTRAINT account_intake_staged_credentials_source_kind_check
        CHECK (source_kind IN ('claude_cookie', 'claude_setup_cookie', 'crs_sync', 'oauth')),
    ADD CONSTRAINT account_intake_staged_credentials_status_check
        CHECK (status IN ('oauth_pending', 'oauth_exchanged', 'staged', 'claimed', 'completed', 'failed', 'expired')),
    ADD CONSTRAINT account_intake_staged_source_mode_check CHECK (
        (source_kind IN ('claude_cookie', 'claude_setup_cookie')
            AND vendor = 'anthropic' AND auth_mode IN ('claude_ai_oauth', 'claude_setup_token'))
        OR
        (source_kind = 'crs_sync' AND vendor IN ('anthropic', 'openai', 'gemini')
            AND auth_mode IN ('api_key', 'claude_ai_oauth', 'claude_setup_token',
                              'chatgpt_oauth', 'codex_cli_oauth', 'refresh_token',
                              'aistudio_api_key', 'code_assist', 'google_one'))
        OR
        (source_kind = 'oauth' AND (
            (vendor = 'anthropic' AND auth_mode = 'claude_ai_oauth')
            OR (vendor = 'openai' AND auth_mode IN ('chatgpt_oauth', 'codex_cli_oauth', 'codex_web_oauth'))
            OR (vendor = 'gemini' AND auth_mode IN ('code_assist', 'google_one', 'antigravity', 'oauth'))
            OR (vendor = 'antigravity' AND auth_mode = 'oauth')
            OR (vendor = 'grok' AND auth_mode = 'xai_oauth')
            OR (vendor = 'kimi' AND auth_mode = 'kimi_oauth')
            OR (vendor = 'copilot' AND auth_mode = 'copilot_oauth')
        ))
    ),
    ADD CONSTRAINT account_intake_staged_secret_lifecycle CHECK (
        (status IN ('oauth_pending', 'oauth_exchanged', 'staged')
            AND encrypted_content IS NOT NULL AND encryption_scheme IS NOT NULL
            AND key_id IS NOT NULL AND nonce IS NOT NULL AND aad_hash IS NOT NULL
            AND claimed_at IS NULL AND finished_at IS NULL)
        OR
        (status = 'claimed' AND encrypted_content IS NULL AND encryption_scheme IS NULL
            AND key_id IS NULL AND nonce IS NULL AND aad_hash IS NULL
            AND claimed_at IS NOT NULL AND finished_at IS NULL)
        OR
        (status IN ('completed', 'failed', 'expired') AND encrypted_content IS NULL
            AND encryption_scheme IS NULL AND key_id IS NULL AND nonce IS NULL AND aad_hash IS NULL)
    );

DROP INDEX idx_account_intake_staged_expiry;
CREATE INDEX idx_account_intake_staged_expiry
    ON account_intake_staged_credentials (expires_at)
    WHERE status IN ('oauth_pending', 'oauth_exchanged', 'staged');

COMMENT ON COLUMN credential_acquisition_flow_sessions.provider_account_id IS
    '已有账号凭据获取必填；仅账号创建型 OAuth 在统一导入执行前为空。';

COMMIT;
