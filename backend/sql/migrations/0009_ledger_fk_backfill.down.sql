-- Down migration for 0009.
-- DEV ROLLBACK ONLY — production rollback must export billing_ledger_archive
-- referential integrity report first (any orphan rows after the constraint
-- drop are billing-money-grade audit gaps).

BEGIN;

-- Composite claim_id FKs on tenant-scoped ledger tables (codex N+4b1 pass-2 P1).
ALTER TABLE billing_ledger_adjustments DROP CONSTRAINT IF EXISTS fk_adjustments_original_claim;
ALTER TABLE usage_record_dlq           DROP CONSTRAINT IF EXISTS fk_usage_dlq_claim;
ALTER TABLE billing_events             DROP CONSTRAINT IF EXISTS fk_billing_events_claim;
ALTER TABLE usage_records              DROP CONSTRAINT IF EXISTS fk_usage_claim;
-- Restore the original single-column FKs that the migration replaced.
ALTER TABLE billing_ledger_adjustments
    ADD CONSTRAINT billing_ledger_adjustments_original_claim_id_fkey
        FOREIGN KEY (original_claim_id) REFERENCES billing_ledger_claims (id);
ALTER TABLE usage_record_dlq
    ADD CONSTRAINT usage_record_dlq_claim_id_fkey
        FOREIGN KEY (claim_id) REFERENCES billing_ledger_claims (id);
ALTER TABLE billing_events
    ADD CONSTRAINT billing_events_claim_id_fkey
        FOREIGN KEY (claim_id) REFERENCES billing_ledger_claims (id);
ALTER TABLE usage_records
    ADD CONSTRAINT usage_records_claim_id_fkey
        FOREIGN KEY (claim_id) REFERENCES billing_ledger_claims (id);

ALTER TABLE pool_slot_acquisitions DROP CONSTRAINT IF EXISTS fk_psa_claim;
ALTER TABLE billing_ledger_archive DROP CONSTRAINT IF EXISTS fk_archive_api_key;
ALTER TABLE usage_records          DROP CONSTRAINT IF EXISTS fk_usage_user;
ALTER TABLE usage_records          DROP CONSTRAINT IF EXISTS fk_usage_api_key;
ALTER TABLE billing_ledger_claims  DROP CONSTRAINT IF EXISTS fk_claims_user;
ALTER TABLE billing_ledger_claims  DROP CONSTRAINT IF EXISTS fk_claims_api_key;

DROP INDEX IF EXISTS uq_billing_ledger_claims_tenant_id_id;
DROP INDEX IF EXISTS uq_api_keys_tenant_id_id;

COMMIT;
