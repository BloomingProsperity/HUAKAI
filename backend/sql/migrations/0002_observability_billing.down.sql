-- Down migration for 0002_observability_billing.

BEGIN;

DROP INDEX IF EXISTS idx_pricing_effective;
DROP INDEX IF EXISTS uq_pricing_tenant_version;
DROP TABLE IF EXISTS billing_pricing_versions;

DROP INDEX IF EXISTS idx_adjustments_tenant_type_time;
DROP INDEX IF EXISTS idx_adjustments_claim;
DROP TABLE IF EXISTS billing_ledger_adjustments;

DROP INDEX IF EXISTS idx_reconciliation_tenant_time;
DROP INDEX IF EXISTS idx_reconciliation_original;
DROP TABLE IF EXISTS usage_record_reconciliation_events;

DROP INDEX IF EXISTS idx_usage_dlq_replayed;
DROP INDEX IF EXISTS idx_usage_dlq_tenant_unreplayed;
DROP TABLE IF EXISTS usage_record_dlq;

DROP INDEX IF EXISTS idx_usage_records_account_settled;
DROP INDEX IF EXISTS idx_usage_records_claim;
DROP INDEX IF EXISTS idx_usage_records_pending_reconciliation;
DROP INDEX IF EXISTS idx_usage_records_user_settled;
DROP INDEX IF EXISTS idx_usage_records_tenant_settled;
DROP TABLE IF EXISTS usage_records;

DROP INDEX IF EXISTS idx_billing_events_tenant_type_time;
DROP INDEX IF EXISTS idx_billing_events_claim_time;
DROP TABLE IF EXISTS billing_events;

DROP INDEX IF EXISTS uq_archive_idempotency;
DROP TABLE IF EXISTS billing_ledger_archive;

DROP INDEX IF EXISTS idx_claims_account_settled;
DROP INDEX IF EXISTS idx_claims_user_settled;
DROP INDEX IF EXISTS idx_claims_status_lease;
DROP INDEX IF EXISTS uq_claims_idempotency;
DROP TABLE IF EXISTS billing_ledger_claims;

COMMIT;
