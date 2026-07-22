-- API key expiry sweep queries.
-- Background-only maintenance: materialize already-expired active keys so
-- user/admin listings stop showing stale active status.

-- name: ExpireActiveAPIKeys :execrows
WITH candidates AS (
    SELECT id
    FROM api_keys
    WHERE purpose = 'user'
      AND status = 'active'
      AND expires_at IS NOT NULL
      AND expires_at <= NOW()
      AND deleted_at IS NULL
    ORDER BY expires_at ASC, id ASC
    LIMIT sqlc.arg(batch_limit)::integer
    FOR UPDATE SKIP LOCKED
)
UPDATE api_keys ak
SET status = 'expired',
    updated_at = NOW()
FROM candidates c
WHERE ak.id = c.id
  AND ak.purpose = 'user'
  AND ak.status = 'active'
  AND ak.expires_at IS NOT NULL
  AND ak.expires_at <= NOW()
  AND ak.deleted_at IS NULL;
