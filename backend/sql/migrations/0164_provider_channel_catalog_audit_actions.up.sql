-- 0164: 把 provider/channel 目录 CRUD 的审计 action 与 target_type 加入 admin_audit_events 白名单。
--
-- 背景(全栈实测发现的真 bug):provider_catalog_mutation_handler / channel_catalog_mutation_handler 在
-- 同一事务里写审计 action='create_provider'/'update_provider'/'delete_provider'(target_type='provider')
-- 与 'create_channel'/'update_channel'/'delete_channel'(target_type='channel'),但这些值从未加入
-- admin_audit_events_action_check / admin_audit_events_target_type_check 白名单 → 审计 INSERT 撞 23514
-- CHECK 违反 → 事务回滚 → handler 返 503 → provider/channel 目录的全部增/改/删彻底不可用。
-- 本迁移为纯加性白名单扩展(不动任何数据、不删任何既有值),修复目录 CRUD。

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
        -- 新增:provider / channel 目录 CRUD
        'create_provider', 'update_provider', 'delete_provider',
        'create_channel', 'update_channel', 'delete_channel'
    ]::text[]));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check CHECK (target_type = ANY (ARRAY[
        'api_key', 'admin_token', 'tenant', 'user', 'provider_account', 'account_credential',
        'billing_setting', 'pool_group', 'platform_setting', 'quota_policy', 'dlq_event', 'alert_rule',
        'moderation_keyword',
        -- 新增:provider / channel 目录
        'provider', 'channel'
    ]::text[]));
