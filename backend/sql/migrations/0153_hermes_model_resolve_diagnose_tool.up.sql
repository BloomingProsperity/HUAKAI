-- 0153: 准入新只读诊断工具 model_resolve_diagnose 的工具名。
--
-- model_resolve_diagnose 是 READ-ONLY 工具(诊断一个公开模型别名在租户内怎么解析/路由):被调用时
-- 只经 internal_tool_handler.recordCall 写入 hermes_tool_calls(tool_name 受 CHECK 约束),**不**写
-- admin_audit_events(只有 mutating 工具走 confirm-gated mutate 路径才写 admin_audit_events)。故本
-- 迁移**只扩 hermes_tool_calls.tool_name CHECK 一处**;admin_audit_events.action 的 hermes.tool.*
-- 白名单**有意不动**(该只读工具永不写 admin_audit_events)。与 0152 同构。
--
-- CHECK 不可组合,故 DROP+ADD 并**逐字复现** 0152 的 11 个工具名 + 仅新增 model_resolve_diagnose,
-- 保留每一个既有工具名不变。

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
            'model_resolve_diagnose'));

COMMIT;
