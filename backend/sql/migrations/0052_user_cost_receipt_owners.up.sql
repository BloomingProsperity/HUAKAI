BEGIN;

-- D-010 (Owner 2026-05-23 schema gate 批): billing_ledger_claims (tenant_id, id, user_id)
-- superset UNIQUE 加固 — 现有 0009 (tenant_id, id) UNIQUE 零碰撞加一列,
-- 让 sidecar 能以 composite FK 强制 sidecar.user_id == claim.user_id DB 层一致性。
-- 复合唯一键让 sidecar 的外键在数据库层同时校验 tenant_id、claim_id 和 user_id。
CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_ledger_claims_tenant_id_id_user_id
    ON billing_ledger_claims (tenant_id, id, user_id);

CREATE TABLE IF NOT EXISTS user_cost_receipt_owners (
    tenant_id        BIGINT NOT NULL,
    request_id       TEXT NOT NULL,
    receipt_sequence INTEGER NOT NULL CHECK (receipt_sequence >= 0),
    user_id          BIGINT NOT NULL,
    claim_id         BIGINT NOT NULL,
    owner_source     TEXT NOT NULL CHECK (owner_source IN ('settle', 'cache_hit', 'backfill_join')),
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, request_id, receipt_sequence),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, request_id, receipt_sequence)
        REFERENCES user_cost_receipts(tenant_id, request_id, receipt_sequence) ON DELETE RESTRICT,
    -- composite FK 同时验 claim 存在 + sidecar.user_id == claim.user_id (D-010 加固)。
    FOREIGN KEY (tenant_id, claim_id, user_id)
        REFERENCES billing_ledger_claims(tenant_id, id, user_id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_user_cost_receipt_owners_user_lookup
    ON user_cost_receipt_owners(tenant_id, user_id, request_id, receipt_sequence DESC);

DROP TRIGGER IF EXISTS enforce_user_cost_receipt_owners_append_only_update ON user_cost_receipt_owners;
CREATE TRIGGER enforce_user_cost_receipt_owners_append_only_update
    BEFORE UPDATE ON user_cost_receipt_owners
    FOR EACH ROW
    EXECUTE FUNCTION enforce_audit_append_only();

DROP TRIGGER IF EXISTS enforce_user_cost_receipt_owners_append_only_delete ON user_cost_receipt_owners;
CREATE TRIGGER enforce_user_cost_receipt_owners_append_only_delete
    BEFORE DELETE ON user_cost_receipt_owners
    FOR EACH ROW
    EXECUTE FUNCTION enforce_audit_append_only();

COMMIT;
