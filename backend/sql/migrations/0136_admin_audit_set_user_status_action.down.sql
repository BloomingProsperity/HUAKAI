BEGIN;
-- 回滚到 0121 的 action 集合(移除 set_user_status)。down 前提:无 action='set_user_status'
-- 的存量行(否则 ADD CHECK 失败)——回滚窗口内若已写入须先清理,与本仓既有
-- audit-action 迁移的 down 约定一致。
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
             'unlock_user', 'force_disable_2fa', 'reset_passkey', 'set_user_group', 'set_user_remark'));
COMMIT;
