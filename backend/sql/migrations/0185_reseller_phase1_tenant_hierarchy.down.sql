-- 仅供开发与测试回滚；生产环境应保持 append-only 并以前滚迁移修复。
BEGIN;

DROP TABLE IF EXISTS tenant_provider_account_allocations;

DROP TRIGGER IF EXISTS tenants_parent_tenant_id_immutable ON tenants;

DROP INDEX IF EXISTS idx_tenants_parent_active;

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_reseller_shape_check,
    DROP CONSTRAINT IF EXISTS tenants_transparency_mode_check,
    DROP CONSTRAINT IF EXISTS tenants_announcement_mode_check,
    DROP CONSTRAINT IF EXISTS tenants_domain_mode_check,
    DROP CONSTRAINT IF EXISTS tenants_upstream_mode_check,
    DROP CONSTRAINT IF EXISTS tenants_parent_not_self_check,
    DROP CONSTRAINT IF EXISTS tenants_parent_tenant_id_fkey;

DROP FUNCTION IF EXISTS reject_tenant_parent_change();

ALTER TABLE tenants
    DROP COLUMN IF EXISTS wholesale_multiplier,
    DROP COLUMN IF EXISTS transparency_mode,
    DROP COLUMN IF EXISTS announcement_mode,
    DROP COLUMN IF EXISTS domain_mode,
    DROP COLUMN IF EXISTS upstream_mode,
    DROP COLUMN IF EXISTS parent_tenant_id;

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
        'update_quota_policy', 'delete_quota_policy', 'clear_provider_account_rate_limit', 'update_provider_account',
        'hermes.tool.dlq_replay', 'hermes.tool.account_pause', 'hermes.tool.account_resume',
        'hermes.tool.renew_trigger', 'hermes.tool.alert_rule_enable', 'hermes.tool.alert_rule_disable',
        'hermes.tool.moderation_keyword_enable', 'hermes.tool.moderation_keyword_disable',
        'create_provider', 'update_provider', 'delete_provider',
        'create_channel', 'update_channel', 'delete_channel',
        'resolve_credential_project',
        'cleanup_runtime_logs'
    ]::text[]));

COMMIT;
