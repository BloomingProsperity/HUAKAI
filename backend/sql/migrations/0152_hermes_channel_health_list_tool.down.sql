-- 0152 down: 把 hermes_tool_calls.tool_name CHECK 还原到 0152 之前(0146)的 10 工具名状态,
-- 移除 channel_health_list。逐字复现 0146 的 IN-list。

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
            'renew_trigger'));

COMMIT;
