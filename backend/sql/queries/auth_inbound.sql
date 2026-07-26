-- 入站鉴权查询。key_hash 只供 resolver 做 bcrypt 比对并立即丢弃，绝不进入
-- 日志或 trace。遥测写入保持 best-effort，不得因遥测失败拒绝有效凭据。

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
    AND u.principal_kind = 'human'
    AND u.role = 'user'
INNER JOIN tenants t
    ON t.id = ak.tenant_id
    AND t.deleted_at IS NULL
WHERE ak.key_prefix = sqlc.arg(key_prefix)
  AND ak.deleted_at IS NULL
  AND ak.status = 'active'
  AND ak.revoked_at IS NULL
  AND ak.purpose = 'user'
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
  AND principal_kind = 'human'
  AND deleted_at IS NULL;
