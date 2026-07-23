-- HUAKAI 限流与冷却基础结构
-- ============================================================================
-- 提供 F-RATE-001 所需表面。
--
-- Most F-RATE-001 state lives on provider_accounts (already locked in
-- pool-routing.sql). This fragment ALTERs that table to add cooldown-state
-- columns + adds new tables for the 6-state machine + audit trail.
-- ============================================================================

-- ----------------------------------------------------------------------------
-- ALTER TABLE: provider_accounts — F-RATE-001 cooldown state columns
-- ----------------------------------------------------------------------------
-- 账号健康状态：
--   active | error | disabled | rate_limited | overloaded |
--   temp_unschedulable | model_rate_limited (note: model is tracked
--   via separate model_rate_limits table, NOT a top-level state)
--
-- Account top-level state derives from these timestamp columns + the
-- existing health_state column from pool-routing.sql. NULL/now-past =
-- not in that state.
-- ----------------------------------------------------------------------------
ALTER TABLE provider_accounts
    -- Rate-limit state (F-RATE-001 §Phase C)
    ADD COLUMN IF NOT EXISTS rate_limited_at         timestamptz,
    ADD COLUMN IF NOT EXISTS rate_limit_reset_at     timestamptz,
    ADD COLUMN IF NOT EXISTS rate_limit_reason       text
                                CHECK (rate_limit_reason IS NULL OR rate_limit_reason IN
                                    ('rate_limit_5h_exceeded', 'rate_limit_7d_exceeded',
                                     'rate_limit_both_windows', 'rate_limit_rpm',
                                     'rate_limit_tpm')),
    -- Overloaded state (F-RATE-001 §Phase D)
    ADD COLUMN IF NOT EXISTS overload_until          timestamptz,
    -- Temp-unschedulable state (F-RATE-001 §Phase E)
    ADD COLUMN IF NOT EXISTS temp_unschedulable_until timestamptz,
    ADD COLUMN IF NOT EXISTS temp_unschedulable_reason text,
    ADD COLUMN IF NOT EXISTS temp_unschedulable_rule_index integer,    -- which rule matched, if applicable
    -- 5h/1d/7d session windows (Anthropic-specific tracking)
    ADD COLUMN IF NOT EXISTS session_window_5h_start timestamptz,
    ADD COLUMN IF NOT EXISTS session_window_5h_end   timestamptz,
    ADD COLUMN IF NOT EXISTS session_window_5h_status text,
    -- OpenAI 403 counter (F-RATE-001 Phase B handle403)
    ADD COLUMN IF NOT EXISTS openai_403_counter      integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS openai_403_window_start timestamptz,
    -- Custom error code policy (F-RATE-001 Phase A)
    ADD COLUMN IF NOT EXISTS custom_error_codes_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS custom_error_codes      integer[] NOT NULL DEFAULT ARRAY[]::integer[],
    -- Pool-mode behavior (F-RATE-001 Phase A short-circuit)
    ADD COLUMN IF NOT EXISTS pool_mode               boolean NOT NULL DEFAULT false,
    -- Temp-unsched rules (operator-configured per-account)
    ADD COLUMN IF NOT EXISTS temp_unschedulable_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS temp_unschedulable_rules jsonb NOT NULL DEFAULT '[]'::jsonb,
        -- jsonb shape per rule: { error_code, keywords[], duration_minutes, description }
    -- Model-level rate limit (F-RATE-001 §1.8 Antigravity-specific granularity)
    ADD COLUMN IF NOT EXISTS model_rate_limits       jsonb NOT NULL DEFAULT '{}'::jsonb,
        -- jsonb map: { "<mapped_model_key>": { rate_limit_reset_at: "<RFC3339>", reason: text } }
    -- Refresh attempt counter (HUAKAI bound)
    ADD COLUMN IF NOT EXISTS refresh_attempt_count   integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS refresh_attempt_window_start timestamptz;

CREATE INDEX IF NOT EXISTS idx_provider_accounts_rate_limit_reset
    ON provider_accounts (rate_limit_reset_at)
    WHERE rate_limit_reset_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_provider_accounts_overload_until
    ON provider_accounts (overload_until)
    WHERE overload_until IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_provider_accounts_temp_unsched_until
    ON provider_accounts (temp_unschedulable_until)
    WHERE temp_unschedulable_until IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_provider_accounts_pool_mode
    ON provider_accounts (pool_mode)
    WHERE pool_mode = true;

COMMENT ON COLUMN provider_accounts.rate_limited_at IS 'F-RATE-001: when current rate-limit state began. NULL = not rate-limited.';
COMMENT ON COLUMN provider_accounts.rate_limit_reset_at IS 'F-RATE-001: when account auto-recovers from rate-limit. now() < reset_at = currently rate-limited.';
COMMENT ON COLUMN provider_accounts.rate_limit_reason IS 'F-RATE-001 §1.4: structured reason for current rate-limit (5h/7d/both/RPM/TPM).';
COMMENT ON COLUMN provider_accounts.overload_until IS 'F-RATE-001 §Phase D: 529 overload cooldown end. Distinct from rate-limit.';
COMMENT ON COLUMN provider_accounts.temp_unschedulable_until IS 'F-RATE-001 §Phase E: temp-unsched end (OAuth refresh window, custom rule match, or refresh-retry-exhaustion).';
COMMENT ON COLUMN provider_accounts.openai_403_counter IS 'F-RATE-001 Phase B handle403: OpenAI 403 counter; permanent disable at >=3 within 180-min window.';
COMMENT ON COLUMN provider_accounts.custom_error_codes_enabled IS 'F-RATE-001 Phase A: when true, only listed status codes trigger state change.';
COMMENT ON COLUMN provider_accounts.pool_mode IS 'F-RATE-001 Phase A: when true, uncustomized errors do NOT mutate local state.';
COMMENT ON COLUMN provider_accounts.temp_unschedulable_rules IS 'F-RATE-001 §1.6: per-account match rules; jsonb array of { error_code, keywords[], duration_minutes, description }.';
COMMENT ON COLUMN provider_accounts.model_rate_limits IS 'F-RATE-001 §1.8: Antigravity-specific per-model cooldown map.';
COMMENT ON COLUMN provider_accounts.refresh_attempt_count IS 'HUAKAI bound: max N OAuth refresh attempts per window before permanent disable.';

-- ----------------------------------------------------------------------------
-- Table: rate_limit_audit_events
-- ----------------------------------------------------------------------------
-- Audit trail for state mutations. Append-only.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rate_limit_audit_events (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    provider_account_id         bigint      NOT NULL REFERENCES provider_accounts(id),
    event_type                  text        NOT NULL CHECK (event_type IN
                                    ('rate_limited_set', 'rate_limited_cleared',
                                     'overloaded_set', 'overloaded_cleared',
                                     'temp_unsched_set', 'temp_unsched_cleared',
                                     'permanent_disable_set', 'manual_clear',
                                     'cascade_clear', 'model_rate_limit_set',
                                     'model_rate_limit_cleared', 'openai_403_counter_increment',
                                     'oauth_401_force_refresh', 'refresh_attempt_exhausted_disable')),
    rate_limit_reason           text,        -- one of the 19 enum strings from spec §Failure Path
    upstream_status_code        integer,
    upstream_request_id         text,
    payload                     jsonb       NOT NULL DEFAULT '{}'::jsonb,
        -- jsonb shape per event_type, e.g. { reset_at, source_layer, headers_summary }
    actor_id                    text,        -- operator id when manual_clear
    occurred_at                 timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_rate_limit_audit_account_time
    ON rate_limit_audit_events (provider_account_id, occurred_at DESC);
CREATE INDEX idx_rate_limit_audit_tenant_type_time
    ON rate_limit_audit_events (tenant_id, event_type, occurred_at DESC);
CREATE INDEX idx_rate_limit_audit_reason_time
    ON rate_limit_audit_events (rate_limit_reason, occurred_at DESC)
    WHERE rate_limit_reason IS NOT NULL;
COMMENT ON TABLE rate_limit_audit_events IS 'F-RATE-001: append-only audit trail for state mutations. Operator dashboard query source.';

-- ----------------------------------------------------------------------------
-- Constraint: ensure pool_routing_audit_events also covers F-RATE state transitions
-- ----------------------------------------------------------------------------
-- The pool_routing_audit_events table from pool-routing.sql does NOT cover
-- rate_limit transitions. This new table handles those exclusively.
-- Cross-table dashboard queries union both audit tables when needed.
-- ----------------------------------------------------------------------------

-- ----------------------------------------------------------------------------
-- Schema lock metadata
-- ----------------------------------------------------------------------------
-- 固化日期：2026-04-28
-- 迁移顺序：0004（在账号池、观测计费和流式转发之后）。
-- ----------------------------------------------------------------------------
