BEGIN;

DROP INDEX IF EXISTS hermes_audit_events_correlation;
DROP INDEX IF EXISTS hermes_audit_events_tenant_ts;
DROP TABLE IF EXISTS hermes_audit_events;

DROP TABLE IF EXISTS hermes_settings;

DROP INDEX IF EXISTS hermes_api_profiles_owner;
DROP INDEX IF EXISTS hermes_api_profiles_tenant;
DROP TABLE IF EXISTS hermes_api_profiles;

DROP INDEX IF EXISTS api_keys_purpose_partial;
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_tenant_user_id_key;
ALTER TABLE api_keys DROP COLUMN IF EXISTS purpose;

COMMIT;
