-- 0016_account_credentials.up.sql
--
-- F-AUTH-005 Round 2-B: upstream credential management v2.
-- 0012 已被 provider account proxy_url 占用，且 0015 已被 OBS DLQ lane
-- 占用；本迁移使用下一版本 0016，避免 golang-migrate duplicate version。

BEGIN;

CREATE TABLE IF NOT EXISTS account_credentials (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    provider_account_id         bigint      NOT NULL REFERENCES provider_accounts(id),
    vendor                      text        NOT NULL,
    auth_mode                   text        NOT NULL,
    state                       text        NOT NULL DEFAULT 'active'
        CHECK (state IN (
            'active',
            'refreshing',
            'refreshing_with_grace',
            'expired',
            'temp_unschedulable',
            'needs_rotation',
            'revoked',
            'operator_attention'
        )),
    credential_version          integer     NOT NULL DEFAULT 1 CHECK (credential_version > 0),
    encrypted_payload           bytea       NOT NULL,
    encryption_scheme           text        NOT NULL DEFAULT 'aes-256-gcm'
        CHECK (encryption_scheme IN ('aes-256-gcm')),
    key_id                      text        NOT NULL,
    nonce                       bytea       NOT NULL,
    aad_hash                    text        NOT NULL,
    payload_fingerprint         text,
    refresh_token_fingerprint   text,
    access_expires_at           timestamptz,
    refresh_expires_at          timestamptz,
    refresh_before_at           timestamptz,
    grace_until                 timestamptz,
    last_refresh_at             timestamptz,
    last_refresh_outcome        text,
    failure_class               text,
    failure_count               integer     NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    next_attempt_at             timestamptz,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    deleted_at                  timestamptz,
    created_by_actor            text,
    last_modified_by_actor      text,
    CONSTRAINT account_credentials_vendor_mode_check CHECK (
        (vendor = 'anthropic' AND auth_mode IN
            ('api_key', 'claude_ai_oauth', 'claude_code', 'bedrock', 'vertex_anthropic'))
        OR
        (vendor = 'openai' AND auth_mode IN
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'azure', 'refresh_token'))
        OR
        (vendor = 'gemini' AND auth_mode IN
            ('aistudio_api_key', 'vertex_sa', 'code_assist', 'google_one', 'antigravity'))
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_account_credentials_active_mode
    ON account_credentials (tenant_id, provider_account_id, vendor, auth_mode)
    WHERE deleted_at IS NULL
      AND state IN ('active', 'refreshing', 'refreshing_with_grace', 'temp_unschedulable', 'needs_rotation', 'operator_attention');

CREATE INDEX IF NOT EXISTS idx_account_credentials_refresh
    ON account_credentials (refresh_before_at, next_attempt_at)
    WHERE deleted_at IS NULL
      AND state IN ('active', 'refreshing_with_grace', 'temp_unschedulable')
      AND refresh_before_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_account_credentials_account
    ON account_credentials (tenant_id, provider_account_id, updated_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_account_credentials_state
    ON account_credentials (tenant_id, state, updated_at DESC)
    WHERE deleted_at IS NULL;

COMMENT ON TABLE account_credentials IS
    'F-AUTH-005: encrypted upstream credential payloads keyed by provider account, vendor, and auth mode. provider_accounts.credentials remains legacy fallback only.';
COMMENT ON COLUMN account_credentials.encrypted_payload IS
    'AES-256-GCM ciphertext. Plaintext must never be logged, audited, or returned by admin APIs.';
COMMENT ON COLUMN account_credentials.aad_hash IS
    'SHA-256 hash of AES-GCM additional authenticated data binding tenant/account/vendor/auth_mode/version/key_id.';
COMMENT ON COLUMN account_credentials.refresh_before_at IS
    'OCAW-34: refresh scheduler scans this timestamp, normally access_expires_at minus 15 minutes.';

CREATE TABLE IF NOT EXISTS credential_audit_events (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    provider_account_id         bigint      NOT NULL REFERENCES provider_accounts(id),
    account_credential_id       bigint      REFERENCES account_credentials(id),
    event_type                  text        NOT NULL CHECK (event_type IN
        ('credential_created', 'credential_rotated', 'credential_disabled',
         'credential_deleted', 'credential_resolved', 'credential_refresh_succeeded',
         'credential_refresh_failed')),
    vendor                      text,
    auth_mode                   text,
    credential_version          integer,
    actor_id                    text,
    request_id                  text,
    payload                     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at                 timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_credential_audit_account_time
    ON credential_audit_events (tenant_id, provider_account_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_credential_audit_credential_time
    ON credential_audit_events (account_credential_id, occurred_at DESC)
    WHERE account_credential_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_credential_audit_event_time
    ON credential_audit_events (event_type, occurred_at DESC);

COMMENT ON TABLE credential_audit_events IS
    'F-TRUST / F-AUTH-005: plaintext-free audit trail for credential create, rotate, disable, delete, resolve, and refresh events.';

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
             'list_account_credentials'));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user',
             'provider_account', 'account_credential'));

COMMIT;
