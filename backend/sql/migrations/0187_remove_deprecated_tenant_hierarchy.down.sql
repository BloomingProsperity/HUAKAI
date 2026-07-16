-- 仅为 migration roundtrip 恢复 0185 的历史结构。
-- 生产环境不应回滚到该版本；运行时不会重新接入递归租户能力。
BEGIN;

ALTER TABLE tenants
    ADD COLUMN parent_tenant_id bigint,
    ADD COLUMN upstream_mode text NOT NULL DEFAULT 'shared_pool',
    ADD COLUMN domain_mode text NOT NULL DEFAULT 'platform_domain',
    ADD COLUMN announcement_mode text NOT NULL DEFAULT 'platform',
    ADD COLUMN transparency_mode text NOT NULL DEFAULT 'masked',
    ADD COLUMN wholesale_multiplier numeric(20,8);

ALTER TABLE tenants
    ADD CONSTRAINT tenants_parent_tenant_id_fkey
        FOREIGN KEY (parent_tenant_id) REFERENCES tenants(id) ON DELETE RESTRICT,
    ADD CONSTRAINT tenants_parent_not_self_check
        CHECK (parent_tenant_id IS NULL OR parent_tenant_id <> id),
    ADD CONSTRAINT tenants_upstream_mode_check
        CHECK (upstream_mode IN ('shared_pool', 'dedicated_accounts')),
    ADD CONSTRAINT tenants_domain_mode_check
        CHECK (domain_mode IN ('platform_domain', 'custom_domain')),
    ADD CONSTRAINT tenants_announcement_mode_check
        CHECK (announcement_mode IN ('platform', 'tenant')),
    ADD CONSTRAINT tenants_transparency_mode_check
        CHECK (transparency_mode IN ('masked', 'isolated')),
    ADD CONSTRAINT tenants_reseller_shape_check
        CHECK (
            (parent_tenant_id IS NULL AND wholesale_multiplier IS NULL)
            OR
            (
                parent_tenant_id IS NOT NULL
                AND wholesale_multiplier IS NOT NULL
                AND wholesale_multiplier > 0
            )
        );

CREATE FUNCTION reject_tenant_parent_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.parent_tenant_id IS DISTINCT FROM NEW.parent_tenant_id THEN
        RAISE EXCEPTION 'parent_tenant_id 创建后不可修改'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'tenants_parent_tenant_id_immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tenants_parent_tenant_id_immutable
    BEFORE UPDATE OF parent_tenant_id ON tenants
    FOR EACH ROW
    EXECUTE FUNCTION reject_tenant_parent_change();

CREATE INDEX idx_tenants_parent_active
    ON tenants (parent_tenant_id, id)
    WHERE deleted_at IS NULL;

CREATE TABLE tenant_provider_account_allocations (
    consumer_tenant_id bigint NOT NULL,
    owner_tenant_id bigint NOT NULL,
    provider_account_id bigint NOT NULL,
    assigned_by_actor text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tenant_provider_account_allocations_pkey
        PRIMARY KEY (consumer_tenant_id, provider_account_id),
    CONSTRAINT tenant_provider_account_allocations_provider_account_key
        UNIQUE (provider_account_id),
    CONSTRAINT tenant_provider_account_allocations_owner_provider_account_fkey
        FOREIGN KEY (owner_tenant_id, provider_account_id)
        REFERENCES provider_accounts (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT tenant_provider_account_allocations_consumer_owner_check
        CHECK (consumer_tenant_id <> owner_tenant_id)
);

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
        'cleanup_runtime_logs',
        'create_reseller_tenant', 'set_reseller_status', 'set_reseller_wholesale_multiplier',
        'set_reseller_modes', 'set_reseller_upstream_allocation'
    ]::text[]));

COMMIT;
