BEGIN;
-- 加性扩展 admin_audit_events.action 枚举:新增 create_user / delete_user
-- (admin 创建/软删用户)。镜像 0136 的 DROP+ADD CHECK 形状;纯加性,不动既有值。
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
             'update_billing_settings',
             'create_pool_group', 'update_pool_group',
             'update_platform_settings',
             'unlock_user', 'force_disable_2fa', 'reset_passkey', 'set_user_group', 'set_user_remark',
             'set_user_status', 'create_user', 'delete_user'));
COMMIT;
