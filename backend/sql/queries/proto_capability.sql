-- F-PROTO-002 protocol capability matrix queries.
-- Backed by docs/schema/protocol-translation.sql (capability + policy tables).

-- name: ListCapabilityCellsForPair :many
SELECT
    id,
    tenant_id,
    client_protocol,
    upstream_protocol,
    feature_name,
    verdict,
    loss_note,
    verifying_test_id,
    matrix_policy_version,
    last_verified_at
FROM protocol_capability_matrix
WHERE tenant_id = $1
  AND client_protocol = $2
  AND upstream_protocol = $3;

-- name: ListCapabilityCellsForTenant :many
SELECT
    id,
    tenant_id,
    client_protocol,
    upstream_protocol,
    feature_name,
    verdict,
    loss_note,
    verifying_test_id,
    matrix_policy_version,
    last_verified_at
FROM protocol_capability_matrix
WHERE tenant_id = $1
ORDER BY client_protocol, upstream_protocol, feature_name;

-- name: UpsertCapabilityCell :exec
INSERT INTO protocol_capability_matrix (
    tenant_id, client_protocol, upstream_protocol, feature_name,
    verdict, loss_note, verifying_test_id, matrix_policy_version,
    last_verified_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, NOW()
)
ON CONFLICT (tenant_id, client_protocol, upstream_protocol, feature_name) DO UPDATE SET
    verdict = EXCLUDED.verdict,
    loss_note = EXCLUDED.loss_note,
    verifying_test_id = EXCLUDED.verifying_test_id,
    matrix_policy_version = EXCLUDED.matrix_policy_version,
    last_verified_at = NOW(),
    updated_at = NOW();

-- name: ListLossyCellsForOperatorUI :many
SELECT
    client_protocol,
    upstream_protocol,
    feature_name,
    verdict,
    loss_note
FROM protocol_capability_matrix
WHERE tenant_id = $1
  AND verdict <> 'PRESERVED'
ORDER BY client_protocol, upstream_protocol, feature_name;
