-- Slice 2 (N+4b1): Ledger FK backfill.
-- Per docs/plans/2026-05-01-n4b-admin-keys.md §Scope B + DB1.
--
-- N+4a (commit 121db58) added users + api_keys tables behind APIKeyResolver
-- but explicitly deferred the FKs from billing_ledger_claims / usage_records /
-- billing_ledger_archive / pool_slot_acquisitions — synthetic test fixtures
-- using `apiKeyID = tenantID*100 + 1` would have broken under the FKs.
-- This migration closes those gaps. Test fixtures rewritten to seed real
-- rows in the same N+4b1 commit.
--
-- All FKs are composite (tenant_id, X) -> (tenant_id, id) where the parent
-- supports it, mirroring N+4a's uq_users_tenant_id_id pattern (codex
-- synthesized N+4 plan §2.4.4) so cross-tenant misbinding is rejected at
-- the schema layer.
--
-- ON DELETE RESTRICT for all six FKs: billing tables are money-grade audit
-- ledgers; deleting an api_keys/users row referenced from them must fail
-- (per F-OBS-001 §Invariant 4). Operators must use status='revoked', not
-- DELETE.
--
-- Pattern: ADD CONSTRAINT NOT VALID + VALIDATE in same TX. HUAKAI is
-- pre-L0 per blueprint v0.2 (no production data), but the pattern is
-- the production-safe norm and L1 will need it anyway.

BEGIN;

-- ----------------------------------------------------------------------------
-- Composite uniq indexes: required as FK targets.
--   - api_keys.(tenant_id, id) — referenced by claims/usage/archive
--   - billing_ledger_claims.(tenant_id, id) — referenced by
--     pool_slot_acquisitions for cross-tenant defense (codex N+4b1
--     pass-1 P1: single-column claim_id FK still allowed tenant B to
--     bind a slot to tenant A's claim).
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
-- The original migration 0001 marked this FK as deferred (line 178 comment
-- "FK to billing_ledger_claims (locked once F-BILL-001 Released)"). N+4b1
-- closes it.
--
-- Codex N+4b1 pass-1 P1: COMPOSITE FK (not single-column). A tenant B
-- slot row binding to tenant A's claim_id is just as broken as a
-- cross-tenant api_key reference; defend at the schema layer.
-- ----------------------------------------------------------------------------
ALTER TABLE pool_slot_acquisitions
    ADD CONSTRAINT fk_psa_claim
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

-- ----------------------------------------------------------------------------
-- Replace single-column claim_id FKs on tenant-scoped ledger tables with
-- composite (tenant_id, claim_id) FKs. Codex N+4b1 pass-2 P1: the existing
-- usage_records/billing_events/usage_record_dlq/billing_ledger_adjustments
-- claim_id FKs only verify "claim_id exists somewhere"; a row with
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
-- VALIDATE all six in this same TX. Pre-L0 means no production data to
-- walk; if dev DB has stale rows from synthetic-id fixtures, this will
-- ROLLBACK and the operator must clean up — that's the desired behavior
-- (loud failure beats silent corruption).
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
