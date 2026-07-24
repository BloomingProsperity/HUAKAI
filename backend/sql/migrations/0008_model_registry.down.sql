-- Down migration for 0008.
-- DEV ROLLBACK ONLY — do NOT run in production once 0009+ depends on
-- uq_pool_groups_tenant_id_id or on usage_records.snapshot_version.
-- 回滚保持与对应 up migration 相反的依赖顺序。

BEGIN;

ALTER TABLE usage_records DROP COLUMN IF EXISTS snapshot_version;

DROP TABLE IF EXISTS model_registry_capabilities;
DROP TABLE IF EXISTS model_pool_bindings;
DROP TABLE IF EXISTS model_aliases;
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS model_registry_tenant_policies;
DROP TABLE IF EXISTS model_registry_snapshots;

-- The composite uniqueness index on pool_groups was added by 0008 as a
-- prerequisite for the (tenant_id, pool_group_id) FK on model_pool_bindings.
-- Drop it last; this is the index most likely to be reused by future
-- migrations, so production rollback should NOT run this line.
DROP INDEX IF EXISTS uq_pool_groups_tenant_id_id;

COMMIT;
