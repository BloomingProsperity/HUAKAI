-- HUAKAI F-FP-POOL Phase 1.3 sqlc queries — 出口代理池 CRUD。
--
-- 多租户约束 (DR-001/TS-006): 所有 query 以 tenant_id 为第一参数过滤,
-- 跨租户访问被 WHERE 拒绝在 SQL 层。

-- name: ListProxiesByTenant :many
SELECT
    id, tenant_id, name, protocol, host, port,
    auth_username, auth_secret, group_id,
    status, last_check_at, created_at, updated_at
FROM proxies
WHERE tenant_id = sqlc.arg(tenant_id) AND deleted_at IS NULL
ORDER BY id;

-- name: ListActiveProxiesByTenant :many
-- Phase 3 health check worker 用 (选 active 但 last_check_at 老的 ping)。
SELECT
    id, tenant_id, name, protocol, host, port,
    auth_username, auth_secret,
    status, last_check_at, created_at, updated_at
FROM proxies
WHERE tenant_id = sqlc.arg(tenant_id)
  AND deleted_at IS NULL
  AND status = 'active'
ORDER BY id;

-- name: GetProxy :one
SELECT
    id, tenant_id, name, protocol, host, port,
    auth_username, auth_secret, group_id,
    status, last_check_at, created_at, updated_at
FROM proxies
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND deleted_at IS NULL;

-- name: CreateProxy :one
-- auth_secret 应由调用方加密后传入 (HUAKAI credentialstore.KeyProvider)。
-- sqlc 层是字节流, 不强制加密格式 — 业务层负责。
INSERT INTO proxies (
    tenant_id, name, protocol, host, port,
    auth_username, auth_secret, group_id, status
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(name), sqlc.arg(protocol), sqlc.arg(host), sqlc.arg(port),
    sqlc.arg(auth_username), sqlc.arg(auth_secret), sqlc.arg(group_id), sqlc.arg(status)
)
RETURNING
    id, tenant_id, name, protocol, host, port,
    auth_username, auth_secret, group_id,
    status, last_check_at, created_at, updated_at;

-- name: UpdateProxy :one
UPDATE proxies
SET
    name = sqlc.arg(name),
    protocol = sqlc.arg(protocol),
    host = sqlc.arg(host),
    port = sqlc.arg(port),
    auth_username = sqlc.arg(auth_username),
    auth_secret = sqlc.arg(auth_secret),
    group_id = sqlc.arg(group_id),
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND deleted_at IS NULL
RETURNING
    id, tenant_id, name, protocol, host, port,
    auth_username, auth_secret, group_id,
    status, last_check_at, created_at, updated_at;

-- name: SetProxyStatus :exec
-- Phase 3 health check worker 标 dead/active, admin 手动 disable 走这。
UPDATE proxies
SET status = sqlc.arg(status),
    last_check_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND deleted_at IS NULL;

-- name: SoftDeleteProxy :exec
UPDATE proxies
SET deleted_at = NOW(), updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND deleted_at IS NULL;
