BEGIN;

ALTER TABLE quota_policies
    ADD COLUMN model_selector text NOT NULL DEFAULT '*';

ALTER TABLE quota_policies
    ADD CONSTRAINT quota_policies_model_selector_valid
    CHECK (
        model_selector = '*'
        OR (
            model_selector <> ''
            AND model_selector = btrim(model_selector)
            AND model_selector = lower(model_selector)
            AND char_length(model_selector) <= 512
            AND position('*' IN model_selector) = 0
        )
    );

DROP INDEX IF EXISTS uq_quota_policies_live_scope_metric;
CREATE UNIQUE INDEX uq_quota_policies_live_scope_metric
    ON quota_policies (
        tenant_id, scope_kind, scope_id, model_selector, metric,
        window_kind, window_seconds, priority
    )
    WHERE enabled = true AND valid_until IS NULL;

DROP INDEX IF EXISTS idx_quota_policies_tenant_scope_metric;
CREATE INDEX idx_quota_policies_tenant_scope_metric
    ON quota_policies (
        tenant_id, scope_kind, scope_id, model_selector, metric,
        enabled, priority
    );

COMMENT ON COLUMN quota_policies.model_selector IS
    '公开模型别名选择器。* 表示所有模型，其他值为规范化后的精确别名；通配与精确策略累加生效。';

ALTER TABLE quota_reservations
    ADD COLUMN requested_model text NOT NULL DEFAULT '';

UPDATE quota_reservations qr
SET requested_model = lower(btrim(blc.requested_model))
FROM billing_ledger_claims blc
WHERE blc.tenant_id = qr.tenant_id
  AND blc.id = qr.claim_id;

ALTER TABLE quota_reservations
    ADD CONSTRAINT quota_reservations_requested_model_valid
    CHECK (
        requested_model = btrim(requested_model)
        AND requested_model = lower(requested_model)
        AND char_length(requested_model) <= 512
    );

COMMENT ON COLUMN quota_reservations.requested_model IS
    '预留时使用的规范化公开模型别名，参与同一 claim 的幂等重放身份校验。';

COMMIT;
