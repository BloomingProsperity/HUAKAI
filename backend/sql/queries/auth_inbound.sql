-- Inbound auth queries.
--   Queries here MUST NOT return key_hash to logs / traces; the resolver
--   only uses key_hash for bcrypt comparison and discards it.
-- Resolver writes stay limited to best-effort auth telemetry;
--   failed telemetry updates must not reject otherwise valid credentials.

-- name: LookupAPIKeysByPrefix :many
-- Returns active candidates whose key_prefix matches. Capped at 5 to
-- bound bcrypt-verify-fanout DOS via colliding prefixes.
--
-- Joins tenants + users so the resolver can check all three status
-- fields in one DB roundtrip so tenant status is checked together with
-- key/user status. INNER JOIN with deleted_at IS NULL on both
-- parent tables means soft-deleted tenants/users never surface a
-- candidate row at all.
SELECT
    ak.id,
    ak.tenant_id,
    ak.user_id,
    ak.key_hash,
    ak.status        AS key_status,
    ak.expires_at,
    ak.ip_allowlist,
    ak.ip_blacklist,
    ak.allowed_models,
    u.status         AS user_status,
    u.user_group     AS user_group,
    t.status         AS tenant_status
FROM api_keys ak
INNER JOIN users u
    ON u.tenant_id = ak.tenant_id
    AND u.id = ak.user_id
    AND u.deleted_at IS NULL
INNER JOIN tenants t
    ON t.id = ak.tenant_id
    AND t.deleted_at IS NULL
WHERE ak.key_prefix = sqlc.arg(key_prefix)
  AND ak.deleted_at IS NULL
  AND ak.status = 'active'
ORDER BY ak.id
LIMIT 5;

-- name: TouchAPIKeyLastUsed :exec
-- Best-effort auth telemetry update after successful bearer verification.
-- The resolver logs and continues if this write fails.
UPDATE api_keys
SET last_used_at = NOW()
WHERE id = sqlc.arg(id)
  AND deleted_at IS NULL;

-- name: GetUserByID :one
-- Tenant-scoped user lookup. Used by admin/audit queries and by the
-- resolver to confirm user.status = 'active'.
SELECT id, tenant_id, email, display_name, status
FROM users
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
  AND deleted_at IS NULL;
