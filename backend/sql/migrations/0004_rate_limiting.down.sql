-- Down migration for 0004_rate_limiting.

BEGIN;

DROP INDEX IF EXISTS idx_rate_limit_audit_reason_time;
DROP INDEX IF EXISTS idx_rate_limit_audit_tenant_type_time;
DROP INDEX IF EXISTS idx_rate_limit_audit_account_time;
DROP TABLE IF EXISTS rate_limit_audit_events;

DROP INDEX IF EXISTS idx_provider_accounts_pool_mode;
DROP INDEX IF EXISTS idx_provider_accounts_temp_unsched_until;
DROP INDEX IF EXISTS idx_provider_accounts_overload_until;
DROP INDEX IF EXISTS idx_provider_accounts_rate_limit_reset;

ALTER TABLE provider_accounts
    DROP COLUMN IF EXISTS refresh_attempt_window_start,
    DROP COLUMN IF EXISTS refresh_attempt_count,
    DROP COLUMN IF EXISTS model_rate_limits,
    DROP COLUMN IF EXISTS temp_unschedulable_rules,
    DROP COLUMN IF EXISTS temp_unschedulable_enabled,
    DROP COLUMN IF EXISTS pool_mode,
    DROP COLUMN IF EXISTS custom_error_codes,
    DROP COLUMN IF EXISTS custom_error_codes_enabled,
    DROP COLUMN IF EXISTS openai_403_window_start,
    DROP COLUMN IF EXISTS openai_403_counter,
    DROP COLUMN IF EXISTS session_window_5h_status,
    DROP COLUMN IF EXISTS session_window_5h_end,
    DROP COLUMN IF EXISTS session_window_5h_start,
    DROP COLUMN IF EXISTS temp_unschedulable_rule_index,
    DROP COLUMN IF EXISTS temp_unschedulable_reason,
    DROP COLUMN IF EXISTS temp_unschedulable_until,
    DROP COLUMN IF EXISTS overload_until,
    DROP COLUMN IF EXISTS rate_limit_reason,
    DROP COLUMN IF EXISTS rate_limit_reset_at,
    DROP COLUMN IF EXISTS rate_limited_at;

COMMIT;
