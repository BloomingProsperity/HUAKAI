BEGIN;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_billing_effect_check,
    DROP COLUMN IF EXISTS billing_effect;

ALTER TABLE usage_records
    DROP CONSTRAINT IF EXISTS usage_records_billing_effect_check,
    DROP COLUMN IF EXISTS billing_effect;

ALTER TABLE billing_ledger_claims
    DROP CONSTRAINT IF EXISTS billing_ledger_claims_billing_effect_check,
    DROP COLUMN IF EXISTS billing_effect;

COMMIT;
