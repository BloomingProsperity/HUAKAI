BEGIN;

DROP INDEX IF EXISTS idx_api_keys_quota_policy;
DROP INDEX IF EXISTS idx_api_keys_group;

ALTER TABLE api_keys
    DROP CONSTRAINT IF EXISTS fk_api_keys_quota_policy,
    DROP CONSTRAINT IF EXISTS fk_api_keys_key_group;

ALTER TABLE api_keys
    DROP COLUMN IF EXISTS quota_policy_id,
    DROP COLUMN IF EXISTS key_group_id;

DROP INDEX IF EXISTS idx_api_key_groups_tenant_enabled;
DROP INDEX IF EXISTS uq_api_key_groups_tenant_name;
DROP TABLE IF EXISTS api_key_groups;

COMMIT;
