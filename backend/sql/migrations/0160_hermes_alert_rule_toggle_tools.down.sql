-- 0160 down: 把三处 CHECK 还原到 0160 之前的状态 ——
--   (1) hermes_tool_calls.tool_name 还原到 0159 的 19 工具名(移除 alert_rule_enable/disable);
--   (2) admin_audit_events.action 还原到 0146 的完整 action 列表(移除两个 hermes.tool.alert_rule_*);
--   (3) admin_audit_events.target_type 还原到 0146 的列表(移除 alert_rule)。
-- 逐字复现各自 0160 之前的 IN-list。

BEGIN;

ALTER TABLE hermes_tool_calls
    DROP CONSTRAINT IF EXISTS hermes_tool_calls_tool_name_check,
    ADD CONSTRAINT hermes_tool_calls_tool_name_check
        CHECK (tool_name IN (
            'credential_diagnose',
            'account_health_diagnose',
            'request_diagnose',
            'dlq_inspect',
            'audit_lookup',
            'log_analyze',
            'dlq_replay',
            'account_pause',
            'account_resume',
            'renew_trigger',
            'channel_health_list',
            'model_resolve_diagnose',
            'pool_list',
            'provider_account_list',
            'quota_policy_list',
            'alert_rule_list',
            'alert_event_list',
            'provider_catalog_list',
            'channel_catalog_list'));

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
             'clear_provider_account_rate_limit', 'update_provider_account',
             'hermes.tool.dlq_replay', 'hermes.tool.account_pause',
             'hermes.tool.account_resume', 'hermes.tool.renew_trigger'));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user',
             'provider_account', 'account_credential',
             'billing_setting', 'pool_group', 'platform_setting',
             'quota_policy', 'dlq_event'));

COMMIT;
