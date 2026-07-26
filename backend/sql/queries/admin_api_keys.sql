-- 管理侧 api_keys 查询由 internal/admin 调用，与 internal/auth 面向客户的
-- LookupAPIKeysByPrefix 热路径相互独立。管理面不得复用只按前缀查找的热路径，
-- 两者属于不同安全边界。

-- name: AdminInsertAPIKey :one
-- 写入时锁定并确认租户和终端用户仍有效；目标在外部预检后失活或角色已不是
-- user 时返回 NoRows。FOR SHARE 会与生命周期/用户状态的非键更新互斥，避免
-- 停用提交后又从旧快照落下一把新 Key。
WITH eligible_target AS MATERIALIZED (
    SELECT t.id AS tenant_id, u.id AS user_id
    FROM tenants t
    JOIN users u
      ON u.tenant_id = t.id
     AND u.id = sqlc.arg(user_id)::bigint
     AND u.principal_kind = 'human'
     AND u.role = 'user'
     AND u.deleted_at IS NULL
     AND u.status = 'active'
    WHERE t.id = sqlc.arg(tenant_id)::bigint
      AND t.deleted_at IS NULL
      AND t.status = 'active'
    FOR SHARE OF t, u
)
INSERT INTO api_keys (
    tenant_id, user_id, name, key_hash, key_prefix, status, expires_at
)
SELECT
    eligible_target.tenant_id,
    eligible_target.user_id,
    sqlc.arg(name)::text,
    sqlc.arg(key_hash)::text,
    sqlc.arg(key_prefix)::text,
    'active',
    sqlc.narg(expires_at)::timestamptz
FROM eligible_target
RETURNING id, created_at;

-- name: AdminListAPIKeysForTenant :many
-- 列出该租户全部 purpose=user Key 元数据，绝不返回 key_hash。这里不能按
-- users 当前角色或 deleted_at 过滤：持有人升为管理员或被软删后，历史凭据
-- 仍必须留在运维视野中并可被永久撤销。
SELECT
    id, tenant_id, user_id, name, key_prefix, status,
    expires_at, last_used_at, revoked_at, revoked_reason,
    created_at, updated_at
FROM api_keys
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND purpose = 'user'
  AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: AdminRevokeAPIKey :execrows
-- 将终端用户 Key 的任意非 revoked 状态收敛为 revoked。返回 0 行表示已撤销，
-- 持有人的当前角色和删除状态不得阻断永久撤销。
UPDATE api_keys
SET status = 'revoked',
    revoked_at = NOW(),
    revoked_reason = sqlc.arg(reason)::text,
    updated_at = NOW()
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND purpose = 'user'
  AND status <> 'revoked'
  AND deleted_at IS NULL;

-- name: AdminCheckIssuanceTarget :one
-- 在生成 bearer 与 bcrypt 前确认目标是有效终端用户。
SELECT
    EXISTS (
        SELECT 1 FROM tenants t
        WHERE t.id = sqlc.arg(tenant_id)::bigint
          AND t.deleted_at IS NULL
          AND t.status = 'active'
    ) AS tenant_ok,
    EXISTS (
        SELECT 1 FROM users u
        WHERE u.id = sqlc.arg(user_id)::bigint
          AND u.tenant_id = sqlc.arg(tenant_id)::bigint
          AND u.principal_kind = 'human'
          AND u.role = 'user'
          AND u.deleted_at IS NULL
          AND u.status = 'active'
    ) AS user_ok;

-- name: AdminCheckTenantExists :one
-- Codex N+4b2 pass-8 P2: list endpoint must verify the tenant exists
-- before writing the audit row, otherwise the admin_audit_events.tenant_id
-- FK turns "tenant_id=<bogus>" into a 503 audit-write failure instead
-- of a clean 404. Active OR disabled is fine — we just need a valid FK
-- target.
SELECT EXISTS (
    SELECT 1 FROM tenants
    WHERE id = sqlc.arg(tenant_id)::bigint
      AND deleted_at IS NULL
);

-- name: AdminGetAPIKeyByID :one
-- 撤销流程使用的租户级 purpose=user Key 查询。不得依赖持有人当前状态，
-- 否则最需要退役的历史凭据反而会从管理面消失。
SELECT
    id, tenant_id, user_id, name, key_prefix, status,
    expires_at, last_used_at, revoked_at, revoked_reason,
    created_at, updated_at
FROM api_keys
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND purpose = 'user'
  AND deleted_at IS NULL;
