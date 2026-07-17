BEGIN;

ALTER TABLE account_credentials
    DROP CONSTRAINT IF EXISTS account_credentials_vendor_mode_check,
    ADD CONSTRAINT account_credentials_vendor_mode_check CHECK (
        (vendor = 'anthropic' AND auth_mode IN
            ('api_key', 'claude_ai_oauth', 'claude_code', 'claude_setup_token', 'bedrock', 'vertex_anthropic'))
        OR
        (vendor = 'openai' AND auth_mode IN
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'codex_agent_identity', 'azure', 'refresh_token', 'codex_web_oauth'))
        OR
        (vendor = 'gemini' AND auth_mode IN
            ('aistudio_api_key', 'vertex_sa', 'code_assist', 'google_one', 'antigravity', 'oauth'))
        OR
        (vendor = 'grok' AND auth_mode IN ('api_key', 'xai_oauth'))
        OR
        (vendor = 'kimi' AND auth_mode IN ('api_key', 'kimi_oauth'))
        OR
        (vendor = 'copilot' AND auth_mode = 'copilot_oauth')
        OR
        (vendor IN ('antigravity', 'windsurf') AND auth_mode = 'oauth')
        OR
        (vendor IN ('deepseek', 'qwen', 'glm', 'yi', 'baichuan', 'doubao', 'minimax', 'ernie', 'hunyuan', 'step')
            AND auth_mode = 'api_key')
    );

ALTER TABLE credential_acquisition_flow_sessions
    DROP CONSTRAINT IF EXISTS credential_acq_vendor_mode_check,
    ADD CONSTRAINT credential_acq_vendor_mode_check CHECK (
        (vendor = 'anthropic' AND auth_mode IN
            ('api_key', 'claude_ai_oauth', 'claude_code', 'claude_setup_token', 'bedrock', 'vertex_anthropic'))
        OR
        (vendor = 'openai' AND auth_mode IN
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'codex_agent_identity', 'azure', 'refresh_token', 'codex_web_oauth'))
        OR
        (vendor = 'gemini' AND auth_mode IN
            ('aistudio_api_key', 'vertex_sa', 'code_assist', 'google_one', 'antigravity', 'oauth'))
        OR
        (vendor = 'grok' AND auth_mode IN ('api_key', 'xai_oauth'))
        OR
        (vendor = 'kimi' AND auth_mode IN ('api_key', 'kimi_oauth'))
        OR
        (vendor = 'copilot' AND auth_mode = 'copilot_oauth')
        OR
        (vendor IN ('antigravity', 'windsurf') AND auth_mode = 'oauth')
        OR
        (vendor IN ('deepseek', 'qwen', 'glm', 'yi', 'baichuan', 'doubao', 'minimax', 'ernie', 'hunyuan', 'step')
            AND auth_mode = 'api_key')
    );

CREATE TABLE codex_agent_task_bindings (
    id                    bigserial PRIMARY KEY,
    tenant_id             bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    provider_account_id   bigint NOT NULL REFERENCES provider_accounts(id) ON DELETE CASCADE,
    account_credential_id bigint NOT NULL REFERENCES account_credentials(id) ON DELETE CASCADE,
    credential_version    integer NOT NULL CHECK (credential_version > 0),
    runtime_id_hash       char(64) NOT NULL,
    encrypted_task_id     bytea,
    encryption_scheme     text,
    key_id                text,
    nonce                 bytea,
    aad_hash              char(64),
    task_fingerprint      char(64),
    lease_token           text,
    lease_fence           bigint NOT NULL DEFAULT 0 CHECK (lease_fence >= 0),
    lease_expires_at      timestamptz,
    retry_after           timestamptz,
    consecutive_failures  integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    last_error_class      text,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT codex_agent_task_binding_subject_unique UNIQUE
        (tenant_id, provider_account_id, account_credential_id, credential_version),
    CONSTRAINT codex_agent_task_binding_envelope_complete CHECK (
        (encrypted_task_id IS NULL AND encryption_scheme IS NULL AND key_id IS NULL
            AND nonce IS NULL AND aad_hash IS NULL AND task_fingerprint IS NULL)
        OR
        (encrypted_task_id IS NOT NULL AND encryption_scheme IS NOT NULL AND key_id IS NOT NULL
            AND nonce IS NOT NULL AND aad_hash IS NOT NULL AND task_fingerprint IS NOT NULL)
    ),
    CONSTRAINT codex_agent_task_binding_lease_complete CHECK (
        (lease_token IS NULL AND lease_expires_at IS NULL)
        OR (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
    )
);

CREATE INDEX codex_agent_task_bindings_recovery_idx
    ON codex_agent_task_bindings (retry_after, lease_expires_at)
    WHERE encrypted_task_id IS NULL;

COMMIT;
