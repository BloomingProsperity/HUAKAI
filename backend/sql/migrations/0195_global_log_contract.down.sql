BEGIN;

-- 旧版本不能表示 info 级运行日志。降级时映射到旧版本支持的 warn，保留原始行；
-- 分类与可信入库时间等 0195 元数据会随列移除，但 schema 回退不能顺带删除日志。
UPDATE ops_runtime_logs SET level = 'warn' WHERE level = 'info';

DROP INDEX idx_referral_reward_audit_events_ingested_at;
ALTER TABLE referral_reward_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_moderation_log_ingested_at;
ALTER TABLE moderation_log DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_subscription_plan_audit_events_ingested_at;
ALTER TABLE subscription_plan_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_payment_audit_events_ingested_at;
ALTER TABLE payment_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_quota_audit_events_ingested_at;
ALTER TABLE quota_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_rate_limit_audit_events_ingested_at;
ALTER TABLE rate_limit_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_pool_routing_audit_events_ingested_at;
ALTER TABLE pool_routing_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_oauth_refresh_audit_events_ingested_at;
ALTER TABLE oauth_refresh_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_hermes_audit_events_ingested_at;
ALTER TABLE hermes_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_credential_audit_events_ingested_at;
ALTER TABLE credential_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_channel_health_audit_events_ingested_at;
ALTER TABLE channel_health_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_user_audit_events_ingested_at;
ALTER TABLE user_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;
DROP INDEX idx_admin_audit_events_ingested_at;
ALTER TABLE admin_audit_events DROP COLUMN log_category, DROP COLUMN ingested_at;

DROP INDEX idx_ops_runtime_logs_idempotency_key;
DROP INDEX idx_ops_runtime_logs_upstream_request_id;
DROP INDEX idx_ops_runtime_logs_trace_id;
DROP INDEX idx_ops_runtime_logs_tenant_id;
DROP INDEX idx_ops_runtime_logs_event_type_id;
DROP INDEX idx_ops_runtime_logs_category_id;
DROP INDEX idx_ops_runtime_logs_ingested_at;

ALTER TABLE ops_runtime_logs
    DROP CONSTRAINT ops_runtime_logs_recovery_state_check,
    DROP CONSTRAINT ops_runtime_logs_tenant_check,
    DROP CONSTRAINT ops_runtime_logs_actor_kind_check,
    DROP CONSTRAINT ops_runtime_logs_error_code_check,
    DROP CONSTRAINT ops_runtime_logs_error_class_check,
    DROP CONSTRAINT ops_runtime_logs_result_check,
    DROP CONSTRAINT ops_runtime_logs_event_type_check,
    DROP CONSTRAINT ops_runtime_logs_log_category_check,
    DROP CONSTRAINT ops_runtime_logs_level_check,
    DROP COLUMN recovery_state,
    DROP COLUMN idempotency_key,
    DROP COLUMN upstream_request_id,
    DROP COLUMN trace_id,
    DROP COLUMN target_ref,
    DROP COLUMN target_type,
    DROP COLUMN tenant_id,
    DROP COLUMN actor_ref,
    DROP COLUMN actor_kind,
    DROP COLUMN retryable,
    DROP COLUMN error_code,
    DROP COLUMN error_class,
    DROP COLUMN result,
    DROP COLUMN event_type,
    DROP COLUMN log_category,
    DROP COLUMN ingested_at,
    ADD CONSTRAINT ops_runtime_logs_level_check CHECK (level IN ('warn', 'error'));

COMMIT;
