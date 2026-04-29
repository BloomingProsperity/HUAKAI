-- name: InsertSlotAcquisition :one
INSERT INTO pool_slot_acquisitions (
    tenant_id,
    provider_account_id,
    acquisition_token,
    claim_id,
    attempt_seq,
    lease_expires_at
) VALUES (
    sqlc.arg(tenant_id),
    sqlc.arg(provider_account_id),
    sqlc.arg(acquisition_token),
    sqlc.narg(claim_id),
    sqlc.arg(attempt_seq),
    sqlc.arg(lease_expires_at)
)
RETURNING id;

-- name: ReleaseSlotAcquisition :exec
UPDATE pool_slot_acquisitions
SET
    status = 'released_success',
    released_at = NOW(),
    release_reason = sqlc.arg(release_reason)
WHERE acquisition_token = sqlc.arg(acquisition_token)
  AND status = 'acquired';

-- name: ListOrphanedAcquisitions :many
SELECT
    id,
    tenant_id,
    provider_account_id,
    acquisition_token,
    claim_id,
    attempt_seq,
    heartbeat_at,
    lease_expires_at,
    status,
    released_at,
    release_reason,
    created_at
FROM pool_slot_acquisitions
WHERE status = 'acquired'
  AND lease_expires_at < NOW()
ORDER BY lease_expires_at
LIMIT 100;
