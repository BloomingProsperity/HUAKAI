-- codex review HEAD chunk7 P1#3: billing_events / usage_records /
-- usage_record_reconciliation_events / billing_ledger_adjustments 在代码注释里写明 append-only /
-- immutable, 但 DB 层仍允许 UPDATE / DELETE。这条 migration 给这四张表加
-- BEFORE UPDATE/DELETE 触发器, 保证账务 / audit 一致性不可被绕过。

BEGIN;

CREATE OR REPLACE FUNCTION enforce_money_path_append_only() RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION '% is append-only: %', TG_TABLE_NAME, TG_OP;
END;
$$ LANGUAGE plpgsql;

-- billing_events
DROP TRIGGER IF EXISTS billing_events_append_only_update ON billing_events;
CREATE TRIGGER billing_events_append_only_update BEFORE UPDATE ON billing_events
  FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();
DROP TRIGGER IF EXISTS billing_events_append_only_delete ON billing_events;
CREATE TRIGGER billing_events_append_only_delete BEFORE DELETE ON billing_events
  FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();

-- usage_records
DROP TRIGGER IF EXISTS usage_records_append_only_update ON usage_records;
CREATE TRIGGER usage_records_append_only_update BEFORE UPDATE ON usage_records
  FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();
DROP TRIGGER IF EXISTS usage_records_append_only_delete ON usage_records;
CREATE TRIGGER usage_records_append_only_delete BEFORE DELETE ON usage_records
  FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();

-- usage_record_reconciliation_events
DROP TRIGGER IF EXISTS usage_record_reconciliation_events_append_only_update ON usage_record_reconciliation_events;
CREATE TRIGGER usage_record_reconciliation_events_append_only_update BEFORE UPDATE ON usage_record_reconciliation_events
  FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();
DROP TRIGGER IF EXISTS usage_record_reconciliation_events_append_only_delete ON usage_record_reconciliation_events;
CREATE TRIGGER usage_record_reconciliation_events_append_only_delete BEFORE DELETE ON usage_record_reconciliation_events
  FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();

-- billing_ledger_adjustments
DROP TRIGGER IF EXISTS billing_ledger_adjustments_append_only_update ON billing_ledger_adjustments;
CREATE TRIGGER billing_ledger_adjustments_append_only_update BEFORE UPDATE ON billing_ledger_adjustments
  FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();
DROP TRIGGER IF EXISTS billing_ledger_adjustments_append_only_delete ON billing_ledger_adjustments;
CREATE TRIGGER billing_ledger_adjustments_append_only_delete BEFORE DELETE ON billing_ledger_adjustments
  FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();

COMMIT;
