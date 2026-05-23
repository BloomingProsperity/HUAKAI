-- Down migration for 0006_upstream_credential_management.

BEGIN;

DROP INDEX IF EXISTS uq_mimicry_pool;
DROP TABLE IF EXISTS mimicry_policy;

DROP INDEX IF EXISTS uq_oauth_storm_global;
DROP INDEX IF EXISTS uq_oauth_storm_endpoint;
DROP INDEX IF EXISTS uq_oauth_storm_account;
DROP TABLE IF EXISTS oauth_storm_budget;

DROP INDEX IF EXISTS idx_oauth_audit_tenant_outcome_time;
DROP INDEX IF EXISTS idx_oauth_audit_outcome_time;
DROP INDEX IF EXISTS idx_oauth_audit_account_time;
DROP TABLE IF EXISTS oauth_refresh_audit_events;

DROP INDEX IF EXISTS idx_provider_accounts_refresh_at;

ALTER TABLE provider_accounts
    DROP COLUMN IF EXISTS oauth_endpoint_health,
    DROP COLUMN IF EXISTS last_refresh_outcome,
    DROP COLUMN IF EXISTS last_refresh_at,
    DROP COLUMN IF EXISTS refresh_token_fingerprint,
    DROP COLUMN IF EXISTS token_version;

COMMIT;
