-- 0157: 准入新只读诊断工具 alert_rule_list 的工具名。
--
-- alert_rule_list 是 READ-ONLY 工具(列出本租户告警规则):被调用时只经 internal_tool_handler.recordCall
-- 写入 hermes_tool_calls(tool_name 受 CHECK 约束),**不**写 admin_audit_events(只有 mutating 工具走
-- confirm-gated mutate 路径才写)。故本迁移**只扩 hermes_tool_calls.tool_name CHECK 一处**;
-- admin_audit_events.action 有意不动。同 0152-0156。
--
-- CHECK 不可组合,故 DROP+ADD 并**逐字复现** 0156 的 15 个工具名 + 仅新增 alert_rule_list。

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
            'alert_rule_list'));

COMMIT;
