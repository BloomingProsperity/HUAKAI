BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM quota_policies
        WHERE model_selector <> '*'
    ) THEN
        RAISE EXCEPTION
            '不能回滚 0200：仍存在精确模型配额策略，请先显式迁移或删除这些策略';
    END IF;
END
$$;

ALTER TABLE quota_reservations
    DROP CONSTRAINT IF EXISTS quota_reservations_requested_model_valid;
ALTER TABLE quota_reservations
    DROP COLUMN IF EXISTS requested_model;

DROP INDEX IF EXISTS uq_quota_policies_live_scope_metric;
CREATE UNIQUE INDEX uq_quota_policies_live_scope_metric
    ON quota_policies (
        tenant_id, scope_kind, scope_id, metric,
        window_kind, window_seconds, priority
    )
    WHERE enabled = true AND valid_until IS NULL;

DROP INDEX IF EXISTS idx_quota_policies_tenant_scope_metric;
CREATE INDEX idx_quota_policies_tenant_scope_metric
    ON quota_policies (
        tenant_id, scope_kind, scope_id, metric,
        enabled, priority
    );

ALTER TABLE quota_policies
    DROP CONSTRAINT IF EXISTS quota_policies_model_selector_valid;
ALTER TABLE quota_policies
    DROP COLUMN IF EXISTS model_selector;

COMMIT;
