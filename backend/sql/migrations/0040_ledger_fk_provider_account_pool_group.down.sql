BEGIN;

ALTER TABLE audit_refund_pending DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE usage_records DROP CONSTRAINT IF EXISTS usage_records_provider_account_id_fkey;

ALTER TABLE billing_ledger_claims DROP CONSTRAINT IF EXISTS billing_ledger_claims_provider_account_id_fkey;
ALTER TABLE billing_ledger_claims DROP CONSTRAINT IF EXISTS billing_ledger_claims_pooling_group_id_fkey;

COMMIT;
