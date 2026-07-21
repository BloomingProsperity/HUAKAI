BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM credential_acquisition_flow_sessions WHERE provider_account_id IS NULL
    ) THEN
		RAISE EXCEPTION '存在账号创建型 OAuth 会话，不能回滚 0211';
    END IF;
    IF EXISTS (
        SELECT 1 FROM account_intake_staged_credentials
        WHERE source_kind = 'oauth' OR actor_role = 'platform_admin'
    ) THEN
		RAISE EXCEPTION '存在 OAuth 或部署管理员暂存导入记录，不能回滚 0211';
    END IF;
END $$;

ALTER TABLE credential_acquisition_flow_sessions
    ALTER COLUMN provider_account_id SET NOT NULL;

ALTER TABLE account_intake_staged_credentials
    DROP CONSTRAINT account_intake_staged_credentials_actor_role_check,
    DROP CONSTRAINT account_intake_staged_credentials_source_kind_check,
    DROP CONSTRAINT account_intake_staged_credentials_status_check,
    DROP CONSTRAINT account_intake_staged_source_mode_check,
    DROP CONSTRAINT account_intake_staged_secret_lifecycle;

ALTER TABLE account_intake_staged_credentials
    ADD CONSTRAINT account_intake_staged_credentials_actor_role_check
        CHECK (actor_role = 'tenant_operator'),
    ADD CONSTRAINT account_intake_staged_credentials_source_kind_check
        CHECK (source_kind IN ('claude_cookie', 'claude_setup_cookie', 'crs_sync')),
    ADD CONSTRAINT account_intake_staged_credentials_status_check
        CHECK (status IN ('staged', 'claimed', 'completed', 'failed', 'expired')),
    ADD CONSTRAINT account_intake_staged_source_mode_check CHECK (
        (source_kind IN ('claude_cookie', 'claude_setup_cookie')
            AND vendor = 'anthropic' AND auth_mode IN ('claude_ai_oauth', 'claude_setup_token'))
        OR
        (source_kind = 'crs_sync' AND vendor IN ('anthropic', 'openai', 'gemini')
            AND auth_mode IN ('api_key', 'claude_ai_oauth', 'claude_setup_token',
                              'chatgpt_oauth', 'codex_cli_oauth', 'refresh_token',
                              'aistudio_api_key', 'code_assist', 'google_one'))
    ),
    ADD CONSTRAINT account_intake_staged_secret_lifecycle CHECK (
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
    );

DROP INDEX idx_account_intake_staged_expiry;
CREATE INDEX idx_account_intake_staged_expiry
    ON account_intake_staged_credentials (expires_at)
    WHERE status = 'staged';

COMMENT ON COLUMN credential_acquisition_flow_sessions.provider_account_id IS NULL;

COMMIT;
