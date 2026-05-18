BEGIN;

ALTER TABLE user_cost_receipts
    ADD COLUMN IF NOT EXISTS receipt_sequence INTEGER NOT NULL DEFAULT 0
        CHECK (receipt_sequence >= 0);

ALTER TABLE user_cost_receipts
    DROP CONSTRAINT IF EXISTS user_cost_receipts_request_id_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_cost_receipts_request_seq
    ON user_cost_receipts (tenant_id, request_id, receipt_sequence);

COMMENT ON COLUMN user_cost_receipts.receipt_sequence IS
    'F-AUDIT-001 同一 request_id 下的 receipt append-only 序号；0 为 original，后续退款/修正递增。';

COMMIT;

