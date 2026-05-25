BEGIN;

DROP TRIGGER IF EXISTS enforce_user_cost_receipts_append_only_update ON user_cost_receipts;
DROP TRIGGER IF EXISTS enforce_user_cost_receipts_append_only_delete ON user_cost_receipts;
DROP TABLE IF EXISTS user_cost_receipts;
DROP FUNCTION IF EXISTS enforce_audit_append_only();

COMMIT;
