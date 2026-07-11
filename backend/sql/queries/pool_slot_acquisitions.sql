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

-- name: SweepOrphanedSlotAcquisitions :one
WITH candidates AS (
    SELECT id
    FROM pool_slot_acquisitions
    WHERE status = 'acquired'
      AND lease_expires_at < NOW()
      AND NOT EXISTS (
          SELECT 1
          FROM billing_ledger_claims blc
          WHERE blc.id = pool_slot_acquisitions.claim_id
            AND blc.tenant_id = pool_slot_acquisitions.tenant_id
            AND blc.attempt_seq = pool_slot_acquisitions.attempt_seq
            AND blc.status = 'reserving'
      )
      AND NOT EXISTS (
          SELECT 1
          FROM usage_record_dlq d
          WHERE d.tenant_id = pool_slot_acquisitions.tenant_id
            AND d.claim_id = pool_slot_acquisitions.claim_id
            AND d.event_kind = 'post_delivery_settlement'
            AND d.status <> 'delivered'
      )
    ORDER BY lease_expires_at
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
),
swept AS (
    UPDATE pool_slot_acquisitions psa
    SET
        status = 'orphan_swept',
        released_at = NOW(),
        release_reason = sqlc.arg(release_reason)
    FROM candidates c
    WHERE psa.id = c.id
      AND psa.status = 'acquired'
    RETURNING psa.provider_account_id
),
by_account AS (
    SELECT provider_account_id, COUNT(*)::integer AS swept_count
    FROM swept
    GROUP BY provider_account_id
),
decremented AS (
    UPDATE provider_accounts pa
    SET
        in_flight_count = GREATEST(pa.in_flight_count - by_account.swept_count, 0),
        updated_at = NOW()
    FROM by_account
    WHERE pa.id = by_account.provider_account_id
    RETURNING by_account.swept_count
)
SELECT COALESCE(SUM(swept_count), 0)::bigint AS swept_count
FROM decremented;
