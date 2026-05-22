-- 0049_admin_audit_pool_group_action.down.sql
--
-- 回滚 pool group 管理审计 action/target_type 白名单扩展。
-- 若已存在 pool_group 审计行，PostgreSQL 会拒绝回滚；
-- 这是与既有 CHECK 回滚保持一致的保守行为。

BEGIN;

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
             'credential_acquisition_failed', 'credential_acquisition_cancelled',
             'update_billing_settings'));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user',
             'provider_account', 'account_credential',
             'billing_setting'));

COMMIT;
