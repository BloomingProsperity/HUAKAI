-- Ledger FK backfill.
-- This migration adds the tenant-scoped FKs from billing_ledger_claims,
-- usage_records, billing_ledger_archive, and pool_slot_acquisitions to
-- api_keys/users.
--
-- All FKs are composite (tenant_id, X) -> (tenant_id, id) where the parent
-- supports it, so cross-tenant misbinding is rejected at the schema layer.
--
-- ON DELETE RESTRICT for all six FKs: billing tables are money-grade audit
-- ledgers; deleting an api_keys/users row referenced from them must fail
-- Operators must use status='revoked', not
-- DELETE.
--
-- Pattern: ADD CONSTRAINT NOT VALID + VALIDATE in same TX.

BEGIN;

-- ----------------------------------------------------------------------------
-- Composite uniq indexes: required as FK targets.
--   - api_keys.(tenant_id, id) — referenced by claims/usage/archive
--   - billing_ledger_claims.(tenant_id, id) — referenced by
--     pool_slot_acquisitions for cross-tenant defense.
-- Mirrors uq_users_tenant_id_id from migration 0007.
-- ----------------------------------------------------------------------------
CREATE UNIQUE INDEX IF NOT EXISTS uq_api_keys_tenant_id_id
    ON api_keys (tenant_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_ledger_claims_tenant_id_id
    ON billing_ledger_claims (tenant_id, id);

-- ----------------------------------------------------------------------------
-- billing_ledger_claims: composite FKs to api_keys + users.
-- ----------------------------------------------------------------------------
ALTER TABLE billing_ledger_claims
    ADD CONSTRAINT fk_claims_api_key
        FOREIGN KEY (tenant_id, api_key_id)
        REFERENCES api_keys (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

ALTER TABLE billing_ledger_claims
    ADD CONSTRAINT fk_claims_user
        FOREIGN KEY (tenant_id, user_id)
        REFERENCES users (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

-- ----------------------------------------------------------------------------
-- usage_records: composite FKs to api_keys + users.
-- ----------------------------------------------------------------------------
ALTER TABLE usage_records
    ADD CONSTRAINT fk_usage_api_key
        FOREIGN KEY (tenant_id, api_key_id)
        REFERENCES api_keys (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

ALTER TABLE usage_records
    ADD CONSTRAINT fk_usage_user
        FOREIGN KEY (tenant_id, user_id)
        REFERENCES users (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

-- ----------------------------------------------------------------------------
-- billing_ledger_archive: composite FK to api_keys (no user_id column on
-- archive — only api_key_id is recorded for replay-protection lookup).
-- ----------------------------------------------------------------------------
ALTER TABLE billing_ledger_archive
    ADD CONSTRAINT fk_archive_api_key
        FOREIGN KEY (tenant_id, api_key_id)
        REFERENCES api_keys (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

-- ----------------------------------------------------------------------------
-- pool_slot_acquisitions.(tenant_id, claim_id) -> billing_ledger_claims.
-- A tenant B slot row binding to tenant A's claim_id is just as broken as
-- a cross-tenant api_key reference; defend at the schema layer.
-- ----------------------------------------------------------------------------
ALTER TABLE pool_slot_acquisitions
    ADD CONSTRAINT fk_psa_claim
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

-- ----------------------------------------------------------------------------
-- Replace single-column claim_id FKs on tenant-scoped ledger tables with
-- composite (tenant_id, claim_id) FKs. The existing usage_records,
-- billing_events, usage_record_dlq, and billing_ledger_adjustments claim_id
-- FKs only verify "claim_id exists somewhere"; a row with
-- (tenant_id=B, claim_id=A's_claim) satisfies them and corrupts tenant
-- isolation for immutable billing data. Same composite defense as
-- pool_slot_acquisitions above.
-- ----------------------------------------------------------------------------
ALTER TABLE usage_records              DROP CONSTRAINT IF EXISTS usage_records_claim_id_fkey;
ALTER TABLE billing_events             DROP CONSTRAINT IF EXISTS billing_events_claim_id_fkey;
ALTER TABLE usage_record_dlq           DROP CONSTRAINT IF EXISTS usage_record_dlq_claim_id_fkey;
ALTER TABLE billing_ledger_adjustments DROP CONSTRAINT IF EXISTS billing_ledger_adjustments_original_claim_id_fkey;

ALTER TABLE usage_records
    ADD CONSTRAINT fk_usage_claim
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

ALTER TABLE billing_events
    ADD CONSTRAINT fk_billing_events_claim
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

ALTER TABLE usage_record_dlq
    ADD CONSTRAINT fk_usage_dlq_claim
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

ALTER TABLE billing_ledger_adjustments
    ADD CONSTRAINT fk_adjustments_original_claim
        FOREIGN KEY (tenant_id, original_claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

-- ----------------------------------------------------------------------------
-- VALIDATE all constraints in this same TX. If stale cross-tenant rows exist,
-- this will ROLLBACK and the operator must clean up; loud failure beats
-- silent corruption.
-- ----------------------------------------------------------------------------
ALTER TABLE billing_ledger_claims      VALIDATE CONSTRAINT fk_claims_api_key;
ALTER TABLE billing_ledger_claims      VALIDATE CONSTRAINT fk_claims_user;
ALTER TABLE usage_records              VALIDATE CONSTRAINT fk_usage_api_key;
ALTER TABLE usage_records              VALIDATE CONSTRAINT fk_usage_user;
ALTER TABLE billing_ledger_archive     VALIDATE CONSTRAINT fk_archive_api_key;
ALTER TABLE pool_slot_acquisitions     VALIDATE CONSTRAINT fk_psa_claim;
ALTER TABLE usage_records              VALIDATE CONSTRAINT fk_usage_claim;
ALTER TABLE billing_events             VALIDATE CONSTRAINT fk_billing_events_claim;
ALTER TABLE usage_record_dlq           VALIDATE CONSTRAINT fk_usage_dlq_claim;
ALTER TABLE billing_ledger_adjustments VALIDATE CONSTRAINT fk_adjustments_original_claim;

COMMIT;
