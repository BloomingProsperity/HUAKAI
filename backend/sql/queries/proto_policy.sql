-- F-PROTO-002 protocol policy version registry queries.
-- protocol_policy_versions 由 sql/migrations/0005_protocol_translation.up.sql 建立。

-- name: GetActiveProtocolPolicy :one
SELECT
    id,
    tenant_id,
    version,
    policy_data,
    effective_from,
    effective_to,
    created_at,
    created_by_actor
FROM protocol_policy_versions
WHERE tenant_id = $1
  AND effective_from <= NOW()
  AND (effective_to IS NULL OR effective_to > NOW())
ORDER BY effective_from DESC
LIMIT 1;

-- name: GetProtocolPolicyByVersion :one
SELECT
    id,
    tenant_id,
    version,
    policy_data,
    effective_from,
    effective_to,
    created_at,
    created_by_actor
FROM protocol_policy_versions
WHERE tenant_id = $1
  AND version = $2;

-- name: InsertProtocolPolicyVersion :one
INSERT INTO protocol_policy_versions (
    tenant_id, version, policy_data, effective_from, effective_to, created_by_actor
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING id, created_at;

-- name: ExpireCurrentProtocolPolicy :exec
UPDATE protocol_policy_versions
SET effective_to = $2
WHERE tenant_id = $1
  AND effective_to IS NULL;
