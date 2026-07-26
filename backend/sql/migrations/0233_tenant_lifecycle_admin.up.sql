BEGIN;

-- 存量曾把任意非 active 字符串都当作停用态。迁移时收敛为唯一状态机，
-- 避免同一状态在认证、后台任务和运营界面里出现多套名字。
UPDATE tenants
SET status = CASE
        WHEN deleted_at IS NOT NULL THEN 'deleted'
        WHEN status = 'active' THEN 'active'
        ELSE 'disabled'
    END,
    updated_at = now();

ALTER TABLE tenants
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN status_reason TEXT,
    ADD COLUMN status_changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN status_changed_by TEXT,
    ADD CONSTRAINT tenants_version_check CHECK (version > 0),
    ADD CONSTRAINT tenants_status_reason_length_check
        CHECK (status_reason IS NULL OR length(status_reason) <= 1000),
    ADD CONSTRAINT tenants_lifecycle_check CHECK (
        (status IN ('active', 'disabled') AND deleted_at IS NULL)
        OR (status = 'deleted' AND deleted_at IS NOT NULL)
    );

CREATE INDEX idx_tenants_lifecycle
    ON tenants (status, id)
    WHERE deleted_at IS NULL;

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_action_check,
    ADD CONSTRAINT admin_audit_events_action_check CHECK (action = ANY (ARRAY[
        'issue_api_key', 'revoke_api_key', 'list_api_keys', 'issue_admin_token', 'revoke_admin_token',
        'admin_login', 'create_provider_account', 'disable_provider_account', 'enable_provider_account',
        'delete_provider_account', 'create_account_credential', 'rotate_account_credential',
        'disable_account_credential', 'delete_account_credential', 'list_account_credentials',
        'credential_acquisition_started', 'credential_acquisition_completed', 'credential_acquisition_failed',
        'credential_acquisition_cancelled', 'update_billing_settings', 'create_pool_group', 'update_pool_group',
        'delete_pool_group', 'update_platform_settings', 'unlock_user', 'force_disable_2fa', 'reset_passkey',
        'set_user_group', 'set_user_remark', 'set_user_status', 'create_user', 'delete_user',
        'unlink_social_identity', 'create_quota_policy', 'update_quota_policy', 'delete_quota_policy',
        'clear_provider_account_rate_limit', 'recover_provider_account_state', 'update_provider_account',
        'test_provider_account', 'update_model_capability_binding', 'update_user_notification_settings',
        'create_route_rule', 'update_route_rule', 'delete_route_rule',
        'create_proxy', 'update_proxy', 'delete_proxy', 'set_proxy_status',
        'create_model_pool_binding', 'update_model_pool_binding', 'delete_model_pool_binding',
        'create_model_routing_override', 'update_model_routing_override', 'delete_model_routing_override',
        'create_channel_test_template', 'update_channel_test_template', 'delete_channel_test_template',
        'orphan_reconciled', 'orphan_cancelled', 'orphan_ignored',
        'orphan_provider_task_attached', 'orphan_release_requested',
        'hermes.tool.dlq_replay', 'hermes.tool.account_pause', 'hermes.tool.account_resume',
        'hermes.tool.renew_trigger', 'hermes.tool.alert_rule_enable', 'hermes.tool.alert_rule_disable',
        'hermes.tool.moderation_keyword_enable', 'hermes.tool.moderation_keyword_disable',
        'create_provider', 'update_provider', 'delete_provider',
        'create_channel', 'update_channel', 'delete_channel',
        'resolve_credential_project', 'cleanup_runtime_logs',
        'promote_model_discovery', 'ignore_model_discovery',
        'grant_tenant_capability', 'revoke_tenant_capability',
        'account_bundle_exported', 'account_bundle_imported',
        'create_tenant', 'enable_tenant', 'disable_tenant', 'delete_tenant'
    ]::text[]));

COMMIT;
