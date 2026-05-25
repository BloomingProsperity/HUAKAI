-- Down migration for 0005_protocol_translation.

BEGIN;

DROP INDEX IF EXISTS idx_protocol_policy_effective;
DROP INDEX IF EXISTS uq_protocol_policy_tenant_version;
DROP TABLE IF EXISTS protocol_policy_versions;

DROP INDEX IF EXISTS idx_capability_matrix_verdict;
DROP INDEX IF EXISTS uq_capability_matrix_cell;
DROP TABLE IF EXISTS protocol_capability_matrix;

COMMIT;
