BEGIN;
-- 加性扩展 admin_audit_events 白名单(provider account 运维动作补漏):
--   action 新增 clear_provider_account_rate_limit / update_provider_account
-- 镜像 0139(action)的 DROP+ADD CHECK 形状;纯加性,不动既有值。
--
-- latent bug 背景:
--   POST /admin/v1/provider-accounts/{id}/clear-rate-limit 这个已接线的运营
--   "重新上架被冷却 account" 端点在清完冷却列后会写一行 action=
--   'clear_provider_account_rate_limit' 的审计;PATCH /admin/v1/provider-accounts/{id}
--   的更新端点同样写 action='update_provider_account'。但这两个 action 此前从未
--   进入 admin_audit_events_action_check 白名单(最新形态由 0139 定义)。结果:
--   对真实库执行时审计 INSERT 触发 CHECK 违反(SQLSTATE 23514),handler 返回
--   503 audit_write_failed,清冷却 / 更新动作连带回滚 → 运营端点事实上失效。
--   单元测试因 stub 不强制约束而把这个 bug 掩盖了。
-- 不加此白名单两个端点对真实库不可用。
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
             'create_quota_policy', 'update_quota_policy', 'delete_quota_policy',
             'clear_provider_account_rate_limit', 'update_provider_account'));
-- target_type 不动:'provider_account' 已在 0139 白名单内。
COMMIT;
