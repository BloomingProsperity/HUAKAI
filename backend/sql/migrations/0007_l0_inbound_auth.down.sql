-- Down migration for 0007_l0_inbound_auth.

BEGIN;

DROP INDEX IF EXISTS idx_api_keys_expires_at;
DROP INDEX IF EXISTS idx_api_keys_user_status;
DROP INDEX IF EXISTS idx_api_keys_tenant_prefix;
DROP INDEX IF EXISTS idx_api_keys_prefix_active;
DROP TABLE IF EXISTS api_keys;

DROP INDEX IF EXISTS uq_users_tenant_email;
DROP INDEX IF EXISTS idx_users_tenant_status;
DROP INDEX IF EXISTS uq_users_tenant_id_id;
DROP TABLE IF EXISTS users;

COMMIT;
