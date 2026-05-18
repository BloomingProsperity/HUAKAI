BEGIN;

ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'audit_mismatch_refund', 'account_health', 'metrics'));

ALTER TABLE user_cost_receipts
    ADD COLUMN IF NOT EXISTS validation_state text NOT NULL DEFAULT 'valid',
    ADD COLUMN IF NOT EXISTS verdict text NOT NULL DEFAULT 'match',
    ADD COLUMN IF NOT EXISTS adjustment_refs jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE user_cost_receipts
    DROP CONSTRAINT IF EXISTS user_cost_receipts_validation_state_check,
    ADD CONSTRAINT user_cost_receipts_validation_state_check
        CHECK (validation_state IN (
            'valid', 'provisional', 'mismatch_pending', 'mismatch_refunded',
            'not_billable', 'receipt_unavailable'
        )),
    DROP CONSTRAINT IF EXISTS user_cost_receipts_verdict_check,
    ADD CONSTRAINT user_cost_receipts_verdict_check
        CHECK (verdict IN (
            'match', 'substitution_refund', 'mismatch_refund_pending', 'unknown'
        )),
    DROP CONSTRAINT IF EXISTS user_cost_receipts_adjustment_refs_array_check,
    ADD CONSTRAINT user_cost_receipts_adjustment_refs_array_check
        CHECK (jsonb_typeof(adjustment_refs) = 'array');

CREATE TABLE IF NOT EXISTS audit_refund_pending (
    claim_id bigint PRIMARY KEY REFERENCES billing_ledger_claims(id),
    request_id text NOT NULL,
    delta_micro_usd bigint NOT NULL CHECK (delta_micro_usd >= 0),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'completed', 'failed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (
        (status = 'completed' AND completed_at IS NOT NULL)
        OR
        (status <> 'completed' AND completed_at IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_audit_refund_pending_status
    ON audit_refund_pending (status, created_at, claim_id);

COMMENT ON TABLE audit_refund_pending IS
    'F-AUDIT-1-C mismatch verdict 自动退款幂等表；一个 claim 最多完成一个退款 workflow。';
COMMENT ON COLUMN user_cost_receipts.validation_state IS
    'F-AUDIT-001 用户可见 receipt 状态；写入后不 update，只能后续 append 新 receipt / adjustment。';
COMMENT ON COLUMN user_cost_receipts.verdict IS
    'F-AUDIT-001 用户可见 cost verdict。';
COMMENT ON COLUMN user_cost_receipts.adjustment_refs IS
    'F-AUDIT-001 append-only refund/correction 引用列表。';

COMMIT;
