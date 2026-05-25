BEGIN;

DROP TRIGGER IF EXISTS enforce_user_cost_receipt_owners_append_only_update ON user_cost_receipt_owners;
DROP TRIGGER IF EXISTS enforce_user_cost_receipt_owners_append_only_delete ON user_cost_receipt_owners;
DROP INDEX IF EXISTS idx_user_cost_receipt_owners_user_lookup;
DROP TABLE IF EXISTS user_cost_receipt_owners;

-- 回滚 up D-010 加的 billing_ledger_claims superset unique index。
DROP INDEX IF EXISTS uq_billing_ledger_claims_tenant_id_id_user_id;

COMMIT;
