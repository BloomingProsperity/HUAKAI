-- S2-010: credential acquisition flow sessions must bind Provider Account
-- ownership at the database layer, not only during later credential finalization.
--
-- 0040 already creates provider_accounts_tenant_id_id_key, so this migration can
-- replace the old single-column FK with the DR-001 composite FK directly.
-- Existing cross-tenant/orphan flow rows fail fast and must be cleaned before
-- applying this migration.

BEGIN;

ALTER TABLE credential_acquisition_flow_sessions
    DROP CONSTRAINT IF EXISTS credential_acquisition_flow_sessions_provider_account_id_fkey;

ALTER TABLE credential_acquisition_flow_sessions
    ADD CONSTRAINT credential_acquisition_flow_sessions_provider_account_id_fkey
    FOREIGN KEY (tenant_id, provider_account_id) REFERENCES provider_accounts(tenant_id, id);

COMMIT;
