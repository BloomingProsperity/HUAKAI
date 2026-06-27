-- 0160: 准入两个新 MUTATING 工具 alert_rule_enable / alert_rule_disable 的工具名 + 审计动作。
--
-- 二者是 Phase B"扩可提议覆盖面"的首批新增:启用/禁用本租户的一条告警规则(alert rule)。
-- 它们是**可逆的 B 级**运营操作(Proposable=true,LLM 可提议但仍需 operator 确认才执行),
-- 与 0146 的四个 mutating 工具(dlq_replay / account_pause / account_resume / renew_trigger)
-- 同构:每次调用 —— 预览(dry-run)、真正执行、错误、拒绝 —— 都记入 hermes_tool_calls,并镜像
-- 进 admin_audit_events,审计动作为 hermes.tool.<name>。
--
-- 本迁移纯加性 + 可逆,扩**两处** CHECK(CHECK 不可组合,故 DROP+ADD 并逐字复现当前完整列表):
--   (1) hermes_tool_calls.tool_name CHECK —— 逐字复现 0159 的 19 个工具名(当前最新),追加
--       alert_rule_enable、alert_rule_disable。
--   (2) admin_audit_events.action CHECK —— 逐字复现 0146 的完整 action 列表(0146 是最后一个动到
--       action CHECK 的迁移;0152-0159 的只读工具不写 admin_audit_events,故未扩 action),追加
--       hermes.tool.alert_rule_enable、hermes.tool.alert_rule_disable。
--
-- target_type 'alert_rule' 无需加白名单:admin_audit_events.target_type CHECK(0146 复现自 0139)
-- 并不约束告警规则维度;退一步,即便约束,也可在此扩 —— 但当前 target_type CHECK 列表(api_key /
-- admin_token / tenant / user / provider_account / account_credential / billing_setting /
-- pool_group / platform_setting / quota_policy / dlq_event)不含 alert_rule,需一并补上,以免
-- orchestrator 写审计行时违反 target_type CHECK 而回滚翻转。
--
-- .down 把三处 CHECK 还原到 0160 之前的状态。

BEGIN;

-- (1) tool_name CHECK:逐字复现 0159 的 19 工具名 + 新增 alert_rule_enable / alert_rule_disable。
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
            'channel_catalog_list',
            'alert_rule_enable',
            'alert_rule_disable'));

-- (2) admin_audit_events.action CHECK:逐字复现 0146 的完整 action 列表 + 新增两个 hermes.tool.<name>。
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
             'hermes.tool.account_resume', 'hermes.tool.renew_trigger',
             'hermes.tool.alert_rule_enable', 'hermes.tool.alert_rule_disable'));

-- (3) admin_audit_events.target_type CHECK:加入 'alert_rule'(alert_rule_enable/disable 写审计行
-- 时记录的 target_type)。逐字复现 0146 的列表 + 新增 alert_rule。没有它,orchestrator 写审计行会
-- 违反 target_type CHECK 而在事务内中止翻转。
ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user',
             'provider_account', 'account_credential',
             'billing_setting', 'pool_group', 'platform_setting',
             'quota_policy', 'dlq_event', 'alert_rule'));

COMMIT;
