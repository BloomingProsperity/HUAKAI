BEGIN;

DROP TRIGGER IF EXISTS billing_events_append_only_update ON billing_events;
DROP TRIGGER IF EXISTS billing_events_append_only_delete ON billing_events;
DROP TRIGGER IF EXISTS usage_records_append_only_update ON usage_records;
DROP TRIGGER IF EXISTS usage_records_append_only_delete ON usage_records;
DROP TRIGGER IF EXISTS reconciliation_events_append_only_update ON reconciliation_events;
DROP TRIGGER IF EXISTS reconciliation_events_append_only_delete ON reconciliation_events;
DROP TRIGGER IF EXISTS billing_adjustments_append_only_update ON billing_adjustments;
DROP TRIGGER IF EXISTS billing_adjustments_append_only_delete ON billing_adjustments;
DROP FUNCTION IF EXISTS enforce_money_path_append_only();

COMMIT;
