-- Phase L0 minimum inbound auth queries.
-- Per docs/specs/_invariants/cross-module-boundaries.md CMB-5:
--   queries here MUST NOT return key_hash to logs / traces; the resolver
--   only uses key_hash for bcrypt comparison and discards it.
-- Per CMB-7: this set is read-only (Auth is a read-only layer in N+4a);
--   last_used_at update intentionally omitted, scheduled for N+4b.

-- name: LookupAPIKeysByPrefix :many
-- Returns active candidates whose key_prefix matches. Capped at 5 to
-- bound bcrypt-verify-fanout DOS via colliding prefixes (codex
-- synthesized plan §risk matrix).
--
-- Joins tenants + users so the resolver can check all three status
-- fields in one DB roundtrip (codex pass3 P1: tenant status was missed
-- in the per-row check). INNER JOIN with deleted_at IS NULL on both
-- parent tables means soft-deleted tenants/users never surface a
-- candidate row at all.
SELECT
    ak.id,
    ak.tenant_id,
    ak.user_id,
    ak.key_hash,
    ak.status        AS key_status,
    ak.expires_at,
    u.status         AS user_status,
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

-- name: GetUserByID :one
-- Tenant-scoped user lookup. Used by admin/audit queries (Phase E)
-- and by the resolver to confirm user.status = 'active'.
SELECT id, tenant_id, email, display_name, status
FROM users
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
  AND deleted_at IS NULL;
