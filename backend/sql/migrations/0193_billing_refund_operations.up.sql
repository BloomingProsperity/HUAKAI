BEGIN;

-- 退款请求与账单事件分开留存。幂等键描述“这次退款操作”，audit_request_id
-- 继续只承担审计追踪，避免两种身份再次混用。
CREATE TABLE IF NOT EXISTS billing_refund_operations (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    claim_id                    bigint      NOT NULL,
    idempotency_key             text        NOT NULL,
    request_fingerprint         text        NOT NULL,
    requested_amount_micro_usd  bigint      NOT NULL CHECK (requested_amount_micro_usd >= 0),
    reason                      text        NOT NULL,
    require_exact               boolean     NOT NULL,
    applied_amount_micro_usd    bigint      NOT NULL CHECK (applied_amount_micro_usd >= 0),
    covered_amount_micro_usd    bigint      NOT NULL CHECK (covered_amount_micro_usd >= 0),
    outcome                     text        NOT NULL CHECK (outcome IN (
                                    'applied', 'already_satisfied', 'skipped_zero'
                                )),
    billing_event_id            bigint,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT billing_refund_operations_idempotency_key_check
        CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 256),
    CONSTRAINT billing_refund_operations_request_fingerprint_check
        CHECK (request_fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT billing_refund_operations_reason_check
        CHECK (length(btrim(reason)) BETWEEN 1 AND 512),
    CONSTRAINT billing_refund_operations_result_check CHECK (
        (outcome = 'applied'
            AND applied_amount_micro_usd > 0
            AND covered_amount_micro_usd >= applied_amount_micro_usd
            AND billing_event_id IS NOT NULL)
        OR
        (outcome = 'already_satisfied'
            AND applied_amount_micro_usd = 0
            AND covered_amount_micro_usd > 0
            AND billing_event_id IS NOT NULL)
        OR
        (outcome = 'skipped_zero'
            AND applied_amount_micro_usd = 0
            AND covered_amount_micro_usd = 0
            AND billing_event_id IS NULL)
    ),
    CONSTRAINT uq_billing_refund_operations_tenant_key
        UNIQUE (tenant_id, idempotency_key),
    CONSTRAINT fk_billing_refund_operations_claim
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_events_tenant_claim_id
    ON billing_events (tenant_id, claim_id, id);

ALTER TABLE billing_refund_operations
    ADD CONSTRAINT fk_billing_refund_operations_event
    FOREIGN KEY (tenant_id, claim_id, billing_event_id)
    REFERENCES billing_events (tenant_id, claim_id, id);

CREATE INDEX IF NOT EXISTS idx_billing_refund_operations_claim_time
    ON billing_refund_operations (tenant_id, claim_id, created_at DESC, id DESC);

DROP TRIGGER IF EXISTS billing_refund_operations_append_only_update ON billing_refund_operations;
CREATE TRIGGER billing_refund_operations_append_only_update
    BEFORE UPDATE ON billing_refund_operations
    FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();

DROP TRIGGER IF EXISTS billing_refund_operations_append_only_delete ON billing_refund_operations;
CREATE TRIGGER billing_refund_operations_append_only_delete
    BEFORE DELETE ON billing_refund_operations
    FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();

COMMENT ON TABLE billing_refund_operations IS
    '已接受退款请求的不可变事实；同一租户幂等键必须对应同一规范化请求与稳定结果。';
COMMENT ON COLUMN billing_refund_operations.request_fingerprint IS
    '规范化 tenant、claim、请求金额、原因和精确模式的版本化 SHA-256 摘要。';
COMMENT ON COLUMN billing_refund_operations.billing_event_id IS
    '实际新增退款或已被既有调整满足时所引用的 reconciliation_appended 事件。';

COMMENT ON TABLE cost_disputes IS
    '用户发起的费用争议；批准时在同一事务内写入退款事实、账单调整、余额回补和配额冲减。';

COMMIT;
