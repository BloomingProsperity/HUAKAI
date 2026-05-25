-- 0019_credential_acquisition_flow_sessions.up.sql
--
-- F-CRED-001 Phase B: short-lived credential acquisition flow state.
-- This table stores callback/import/finalize state only. Raw upstream
-- credentials must never be persisted here.

BEGIN;

CREATE TABLE IF NOT EXISTS credential_acquisition_flow_sessions (
    id                              uuid PRIMARY KEY,
    tenant_id                       bigint      NOT NULL REFERENCES tenants(id),
    provider_account_id             bigint      NOT NULL REFERENCES provider_accounts(id),
    vendor                          text        NOT NULL,
    auth_mode                       text        NOT NULL,
    flow_kind                       text        NOT NULL CHECK (flow_kind IN (
        'oauth',
        'cli_import',
        'paste',
        'csv_import',
        'json_import',
        'cloud_bootstrap',
        'token_exchange',
        'setup_token',
        'manual_first'
    )),
    status                          text        NOT NULL DEFAULT 'started' CHECK (status IN (
        'started',
        'waiting_for_user',
        'callback_received',
        'validated',
        'finalized',
        'cancelled',
        'expired',
        'failed'
    )),
    actor_id                        text        NOT NULL,
    actor_role                      text        NOT NULL,
    state_hash                      bytea,
    nonce_hash                      bytea,
    encrypted_pkce_verifier         bytea,
    client_identity_source          text        NOT NULL DEFAULT 'none' CHECK (client_identity_source IN (
        'none',
        'public_cli_client',
        'operator_config',
        'per_account_override',
        'disabled_missing_config'
    )),
    redirect_uri                    text,
    requested_scopes                jsonb       NOT NULL DEFAULT '[]'::jsonb,
    redacted_context                jsonb       NOT NULL DEFAULT '{}'::jsonb,
    long_lived_requested            boolean     NOT NULL DEFAULT false,
    idempotency_key_hash            bytea       NOT NULL,
    result_account_credential_id    bigint      REFERENCES account_credentials(id),
    error_class                     text,
    error_message_redacted          text,
    expires_at                      timestamptz NOT NULL,
    consumed_at                     timestamptz,
    cancelled_at                    timestamptz,
    created_at                      timestamptz NOT NULL DEFAULT now(),
    updated_at                      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT credential_acq_vendor_mode_check CHECK (
        (vendor = 'anthropic' AND auth_mode IN
            ('api_key', 'claude_ai_oauth', 'claude_code', 'bedrock', 'vertex_anthropic'))
        OR
        (vendor = 'openai' AND auth_mode IN
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'azure', 'refresh_token'))
        OR
        (vendor = 'gemini' AND auth_mode IN
            ('aistudio_api_key', 'vertex_sa', 'code_assist', 'google_one', 'antigravity'))
    ),
    CONSTRAINT credential_acq_requested_scopes_array
        CHECK (jsonb_typeof(requested_scopes) = 'array'),
    CONSTRAINT credential_acq_redacted_context_object
        CHECK (jsonb_typeof(redacted_context) = 'object'),
    CONSTRAINT credential_acq_terminal_timestamps
        CHECK (
            (status <> 'finalized' OR consumed_at IS NOT NULL)
            AND (status <> 'cancelled' OR cancelled_at IS NOT NULL)
        )
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_credential_acq_idempotency
    ON credential_acquisition_flow_sessions (tenant_id, provider_account_id, idempotency_key_hash);

CREATE INDEX IF NOT EXISTS idx_credential_acq_account_status
    ON credential_acquisition_flow_sessions (tenant_id, provider_account_id, status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_credential_acq_expiry
    ON credential_acquisition_flow_sessions (status, expires_at)
    WHERE status IN ('started', 'waiting_for_user', 'callback_received', 'validated');

CREATE INDEX IF NOT EXISTS idx_credential_acq_result_credential
    ON credential_acquisition_flow_sessions (result_account_credential_id)
    WHERE result_account_credential_id IS NOT NULL;

COMMENT ON TABLE credential_acquisition_flow_sessions IS
    'F-CRED-001: short-lived acquisition state. Raw tokens, API keys, cookies, private keys, auth codes, and cloud secrets are forbidden.';
COMMENT ON COLUMN credential_acquisition_flow_sessions.state_hash IS
    'Hash of OAuth state; raw state is never stored.';
COMMENT ON COLUMN credential_acquisition_flow_sessions.encrypted_pkce_verifier IS
    'Encrypted PKCE verifier material only; destroyed by expiry/finalization policy.';
COMMENT ON COLUMN credential_acquisition_flow_sessions.redacted_context IS
    'Allowlisted operator preview metadata only. Secret-shaped keys and values are rejected or redacted by application code.';

ALTER TABLE credential_audit_events
    DROP CONSTRAINT IF EXISTS credential_audit_events_event_type_check,
    ADD CONSTRAINT credential_audit_events_event_type_check
        CHECK (event_type IN
            ('credential_created', 'credential_rotated', 'credential_disabled',
             'credential_deleted', 'credential_resolved', 'credential_refresh_succeeded',
             'credential_refresh_failed',
             'credential_acquisition_started', 'credential_acquisition_completed',
             'credential_acquisition_failed', 'credential_acquisition_cancelled',
             'gemini_cross_client_fallback'));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_action_check,
    ADD CONSTRAINT admin_audit_events_action_check
        CHECK (action IN
            ('issue_api_key', 'revoke_api_key', 'list_api_keys',
             'issue_admin_token', 'revoke_admin_token', 'admin_login',
             'create_provider_account', 'disable_provider_account',
             'enable_provider_account', 'delete_provider_account',
             'create_account_credential', 'rotate_account_credential',
             'disable_account_credential', 'delete_account_credential',
             'list_account_credentials',
             'credential_acquisition_started', 'credential_acquisition_completed',
             'credential_acquisition_failed', 'credential_acquisition_cancelled'));

COMMIT;
