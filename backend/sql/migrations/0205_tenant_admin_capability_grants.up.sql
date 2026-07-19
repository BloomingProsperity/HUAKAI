BEGIN;

CREATE TABLE tenant_admin_capability_grants (
    tenant_id    BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    capability   TEXT        NOT NULL CHECK (capability IN ('advanced_account_intake')),
    enabled      BOOLEAN     NOT NULL DEFAULT false,
    updated_by   TEXT        NOT NULL CHECK (length(btrim(updated_by)) > 0),
    reason       TEXT        NOT NULL CHECK (length(btrim(reason)) > 0 AND length(reason) <= 1000),
    granted_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, capability),
    CONSTRAINT tenant_admin_capability_state_check CHECK (
        (enabled AND granted_at IS NOT NULL AND revoked_at IS NULL)
        OR
        (NOT enabled AND revoked_at IS NOT NULL)
    )
);

CREATE INDEX idx_tenant_admin_capability_enabled
    ON tenant_admin_capability_grants (capability, tenant_id)
    WHERE enabled;

COMMENT ON TABLE tenant_admin_capability_grants IS
    '部署管理员授予下级租户管理员的运营能力；缺失记录一律按未授权处理，部署管理员不能借此代替租户执行。';

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_action_check,
    ADD CONSTRAINT admin_audit_events_action_check CHECK (action = ANY (ARRAY[
        'issue_api_key', 'revoke_api_key', 'list_api_keys', 'issue_admin_token', 'revoke_admin_token',
        'admin_login', 'create_provider_account', 'disable_provider_account', 'enable_provider_account',
        'delete_provider_account', 'create_account_credential', 'rotate_account_credential',
        'disable_account_credential', 'delete_account_credential', 'list_account_credentials',
        'credential_acquisition_started', 'credential_acquisition_completed', 'credential_acquisition_failed',
        'credential_acquisition_cancelled', 'update_billing_settings', 'create_pool_group', 'update_pool_group',
        'update_platform_settings', 'unlock_user', 'force_disable_2fa', 'reset_passkey', 'set_user_group',
        'set_user_remark', 'set_user_status', 'create_user', 'delete_user', 'create_quota_policy',
        'update_quota_policy', 'delete_quota_policy', 'clear_provider_account_rate_limit',
        'recover_provider_account_state', 'update_provider_account',
        'hermes.tool.dlq_replay', 'hermes.tool.account_pause', 'hermes.tool.account_resume',
        'hermes.tool.renew_trigger', 'hermes.tool.alert_rule_enable', 'hermes.tool.alert_rule_disable',
        'hermes.tool.moderation_keyword_enable', 'hermes.tool.moderation_keyword_disable',
        'create_provider', 'update_provider', 'delete_provider',
        'create_channel', 'update_channel', 'delete_channel',
        'resolve_credential_project',
        'cleanup_runtime_logs',
        'promote_model_discovery', 'ignore_model_discovery',
        'grant_tenant_capability', 'revoke_tenant_capability'
    ]::text[]));

COMMIT;
