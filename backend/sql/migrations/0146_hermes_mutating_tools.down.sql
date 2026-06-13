BEGIN;

-- Revert admin_audit_events.action whitelist to its 0142 state (drop the four
-- hermes.tool.<name> mutating actions; restore every other action verbatim).
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

-- Revert admin_audit_events.target_type whitelist to its 0139 state (drop
-- 'dlq_event').
ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user',
             'provider_account', 'account_credential',
             'billing_setting', 'pool_group', 'platform_setting',
             'quota_policy'));

-- Drop the dry_run column added for mutating-tool preview tracking.
ALTER TABLE hermes_tool_calls
    DROP COLUMN IF EXISTS dry_run;

-- Revert hermes_tool_calls.tool_name CHECK to its 0145 state (the six H3
-- read-only tools only).
ALTER TABLE hermes_tool_calls
    DROP CONSTRAINT IF EXISTS hermes_tool_calls_tool_name_check,
    ADD CONSTRAINT hermes_tool_calls_tool_name_check
        CHECK (tool_name IN (
            'credential_diagnose',
            'account_health_diagnose',
            'request_diagnose',
            'dlq_inspect',
            'audit_lookup',
            'log_analyze'));

COMMIT;
