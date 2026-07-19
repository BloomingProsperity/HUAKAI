BEGIN;

-- 统一运行日志信封。created_at 保留事件发生时间语义，ingested_at 由数据库生成，
-- 是固定 30 天清理唯一可信的时间轴。
ALTER TABLE ops_runtime_logs
    DROP CONSTRAINT IF EXISTS ops_runtime_logs_level_check,
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'error',
    ADD COLUMN event_type TEXT NOT NULL DEFAULT 'runtime.legacy',
    ADD COLUMN result TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN error_class TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN error_code TEXT NOT NULL DEFAULT 'legacy_unclassified',
    ADD COLUMN retryable BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN actor_kind TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN actor_ref TEXT,
    ADD COLUMN tenant_id BIGINT,
    ADD COLUMN target_type TEXT,
    ADD COLUMN target_ref TEXT,
    ADD COLUMN trace_id TEXT,
    ADD COLUMN upstream_request_id TEXT,
    ADD COLUMN idempotency_key TEXT,
    ADD COLUMN recovery_state TEXT NOT NULL DEFAULT 'none';

-- 存量行没有可证明的原始入库时间，统一从迁移时刻重新起算 30 天，不能用可自报的
-- 事件发生时间伪装成可信入库时间并在迁移后立即误删。
UPDATE ops_runtime_logs
SET ingested_at = clock_timestamp(),
    event_type = CASE level
        WHEN 'error' THEN 'runtime.legacy_error'
        ELSE 'runtime.legacy_warning'
    END,
    result = 'server_failure';

ALTER TABLE ops_runtime_logs
    ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(),
    ALTER COLUMN ingested_at SET NOT NULL,
    ADD CONSTRAINT ops_runtime_logs_level_check
        CHECK (level IN ('info', 'warn', 'error')),
    ADD CONSTRAINT ops_runtime_logs_log_category_check
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery')),
    ADD CONSTRAINT ops_runtime_logs_event_type_check
        CHECK (event_type ~ '^[a-z][a-z0-9_.-]{0,127}$'),
    ADD CONSTRAINT ops_runtime_logs_result_check
        CHECK (result IN ('success', 'denied', 'client_failure', 'server_failure', 'canceled', 'partial', 'timeout', 'unknown')),
    ADD CONSTRAINT ops_runtime_logs_error_class_check
        CHECK (error_class IN ('none', 'validation', 'authentication', 'authorization', 'conflict',
                               'insufficient_balance', 'rate_limit', 'timeout', 'canceled', 'dependency',
                               'data_integrity', 'manual_recovery', 'unknown')),
    ADD CONSTRAINT ops_runtime_logs_error_code_check
        CHECK (error_code ~ '^[a-z][a-z0-9_.-]{0,127}$'),
    ADD CONSTRAINT ops_runtime_logs_actor_kind_check
        CHECK (actor_kind IN ('system', 'platform_admin', 'tenant_admin', 'user', 'api_key', 'unknown')),
    ADD CONSTRAINT ops_runtime_logs_tenant_check
        CHECK (tenant_id IS NULL OR tenant_id > 0),
    ADD CONSTRAINT ops_runtime_logs_recovery_state_check
        CHECK (recovery_state IN ('none', 'pending', 'retrying', 'recovered', 'quarantined',
                                  'operator_required', 'failed'));

CREATE INDEX idx_ops_runtime_logs_ingested_at ON ops_runtime_logs (ingested_at, id);
CREATE INDEX idx_ops_runtime_logs_category_id ON ops_runtime_logs (log_category, id DESC);
CREATE INDEX idx_ops_runtime_logs_event_type_id ON ops_runtime_logs (event_type, id DESC);
CREATE INDEX idx_ops_runtime_logs_tenant_id ON ops_runtime_logs (tenant_id, id DESC) WHERE tenant_id IS NOT NULL;
CREATE INDEX idx_ops_runtime_logs_trace_id ON ops_runtime_logs (trace_id) WHERE trace_id IS NOT NULL;
CREATE INDEX idx_ops_runtime_logs_upstream_request_id ON ops_runtime_logs (upstream_request_id) WHERE upstream_request_id IS NOT NULL;
CREATE INDEX idx_ops_runtime_logs_idempotency_key ON ops_runtime_logs (idempotency_key) WHERE idempotency_key IS NOT NULL;

COMMENT ON COLUMN ops_runtime_logs.created_at IS '事件发生时间，只用于展示和业务关联。';
COMMENT ON COLUMN ops_runtime_logs.ingested_at IS '数据库可信入库时间，固定 30 天保留只使用此列。';
COMMENT ON COLUMN ops_runtime_logs.log_category IS '全局日志分类：operation/financial/security/error/access/recovery。';
COMMENT ON COLUMN ops_runtime_logs.event_type IS '稳定机器事件类型，不使用展示文案作为标识。';

-- 现有领域日志表统一增加数据库可信入库时间。它们仍保留各自的权限与业务字段，
-- 这里只建立全局 30 天生命周期，不把不同权限域的数据汇总暴露。
ALTER TABLE admin_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'operation'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE admin_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE admin_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_admin_audit_events_ingested_at ON admin_audit_events (ingested_at, id);

ALTER TABLE user_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'security'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE user_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE user_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_user_audit_events_ingested_at ON user_audit_events (ingested_at, id);

ALTER TABLE channel_health_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'recovery'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE channel_health_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE channel_health_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_channel_health_audit_events_ingested_at ON channel_health_audit_events (ingested_at, id);

ALTER TABLE credential_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'security'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE credential_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE credential_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_credential_audit_events_ingested_at ON credential_audit_events (ingested_at, id);

ALTER TABLE hermes_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'operation'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE hermes_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE hermes_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_hermes_audit_events_ingested_at ON hermes_audit_events (ingested_at, id);

ALTER TABLE oauth_refresh_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'security'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE oauth_refresh_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE oauth_refresh_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_oauth_refresh_audit_events_ingested_at ON oauth_refresh_audit_events (ingested_at, id);

ALTER TABLE pool_routing_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'operation'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE pool_routing_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE pool_routing_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_pool_routing_audit_events_ingested_at ON pool_routing_audit_events (ingested_at, id);

ALTER TABLE rate_limit_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'recovery'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE rate_limit_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE rate_limit_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_rate_limit_audit_events_ingested_at ON rate_limit_audit_events (ingested_at, id);

ALTER TABLE quota_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'financial'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE quota_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE quota_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_quota_audit_events_ingested_at ON quota_audit_events (ingested_at, id);

ALTER TABLE payment_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'financial'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE payment_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE payment_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_payment_audit_events_ingested_at ON payment_audit_events (ingested_at, id);

ALTER TABLE subscription_plan_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'financial'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE subscription_plan_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE subscription_plan_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_subscription_plan_audit_events_ingested_at ON subscription_plan_audit_events (ingested_at, id);

ALTER TABLE moderation_log
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'security'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE moderation_log SET ingested_at = clock_timestamp();
ALTER TABLE moderation_log ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_moderation_log_ingested_at ON moderation_log (ingested_at, id);

ALTER TABLE referral_reward_audit_events
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'financial'
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery'));
UPDATE referral_reward_audit_events SET ingested_at = clock_timestamp();
ALTER TABLE referral_reward_audit_events ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(), ALTER COLUMN ingested_at SET NOT NULL;
CREATE INDEX idx_referral_reward_audit_events_ingested_at ON referral_reward_audit_events (ingested_at, id);

COMMIT;
