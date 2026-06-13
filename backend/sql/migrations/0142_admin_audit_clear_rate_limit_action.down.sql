BEGIN;
-- 回滚 0141:还原 admin_audit_events 的 action 白名单到 0139 态
-- (移除 clear_provider_account_rate_limit / update_provider_account)。dev/test only。
-- 注意:回滚后这两个 action 的审计写入会重新触发 23514;只在确无相关
-- 运营审计行待写时使用。target_type 未在 0141 改动,故此处不动。
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
             'set_user_status', 'create_user', 'delete_user',
             'create_quota_policy', 'update_quota_policy', 'delete_quota_policy'));
COMMIT;
