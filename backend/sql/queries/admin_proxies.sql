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
    name = CASE WHEN sqlc.arg(name_set)::boolean THEN sqlc.arg(name)::text ELSE name END,
    protocol = CASE WHEN sqlc.arg(protocol_set)::boolean THEN sqlc.arg(protocol)::text ELSE protocol END,
    host = CASE WHEN sqlc.arg(host_set)::boolean THEN sqlc.arg(host)::text ELSE host END,
    port = CASE WHEN sqlc.arg(port_set)::boolean THEN sqlc.arg(port)::integer ELSE port END,
    auth_username = CASE WHEN sqlc.arg(auth_username_set)::boolean THEN sqlc.narg(auth_username)::text ELSE auth_username END,
    auth_secret = CASE WHEN sqlc.arg(auth_secret_set)::boolean THEN sqlc.narg(auth_secret)::text ELSE auth_secret END,
    group_id = CASE WHEN sqlc.arg(group_id_set)::boolean THEN sqlc.narg(group_id)::text ELSE group_id END,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND deleted_at IS NULL
RETURNING
    id, tenant_id, name, protocol, host, port,
    auth_username, auth_secret, group_id,
    status, last_check_at, created_at, updated_at;

-- name: SetProxyStatus :execrows
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

-- name: GetProxyDeleteImpact :one
SELECT
    p.id,
    (
        SELECT COUNT(*)::bigint
        FROM provider_accounts pa
        WHERE pa.tenant_id = p.tenant_id
          AND pa.proxy_id = p.id
          AND pa.deleted_at IS NULL
    ) AS direct_account_count,
    (
        SELECT COUNT(*)::bigint
        FROM tenants t
        WHERE t.id = p.tenant_id
          AND t.default_proxy_id = p.id
    ) AS default_tenant_count,
    (
        SELECT COUNT(*)::bigint
        FROM provider_accounts pa
        WHERE pa.tenant_id = p.tenant_id
          AND pa.proxy_group_id = p.group_id
          AND pa.deleted_at IS NULL
    ) AS group_account_count,
    (
        SELECT COUNT(*)::bigint
        FROM proxies peer
        WHERE peer.tenant_id = p.tenant_id
          AND peer.group_id = p.group_id
          AND peer.id <> p.id
          AND peer.status = 'active'
          AND peer.deleted_at IS NULL
    ) AS group_remaining_active_count
FROM proxies p
WHERE p.tenant_id = sqlc.arg(target_tenant_id)
  AND p.id = sqlc.arg(target_proxy_id)
  AND p.deleted_at IS NULL;

-- name: DeleteProxyIfUnused :one
WITH target AS (
    SELECT base.id, base.tenant_id AS proxy_tenant_id, base.group_id AS proxy_group_id
    FROM proxies base
    WHERE base.tenant_id = sqlc.arg(target_tenant_id)
      AND base.id = sqlc.arg(target_proxy_id)
      AND base.deleted_at IS NULL
    FOR UPDATE
),
impact AS (
    SELECT
        p.id,
        COUNT(DISTINCT direct_account.id)::bigint AS direct_account_count,
        COUNT(DISTINCT default_tenant.id)::bigint AS default_tenant_count,
        COUNT(DISTINCT group_account.id)::bigint AS group_account_count,
        COUNT(DISTINCT peer.id)::bigint AS group_remaining_active_count
    FROM target p
    LEFT JOIN provider_accounts direct_account
      ON direct_account.tenant_id = p.proxy_tenant_id
     AND direct_account.proxy_id = p.id
     AND direct_account.deleted_at IS NULL
    LEFT JOIN tenants default_tenant
      ON default_tenant.id = p.proxy_tenant_id
     AND default_tenant.default_proxy_id = p.id
    LEFT JOIN provider_accounts group_account
      ON group_account.tenant_id = p.proxy_tenant_id
     AND group_account.proxy_group_id = p.proxy_group_id
     AND group_account.deleted_at IS NULL
    LEFT JOIN proxies peer
      ON peer.tenant_id = p.proxy_tenant_id
     AND peer.group_id = p.proxy_group_id
     AND peer.id <> p.id
     AND peer.status = 'active'
     AND peer.deleted_at IS NULL
    GROUP BY p.id
),
deleted AS (
    UPDATE proxies p
    SET deleted_at = NOW(), updated_at = NOW()
    FROM impact i
    WHERE p.id = i.id
      AND i.direct_account_count = 0
      AND i.default_tenant_count = 0
      AND (i.group_account_count = 0 OR i.group_remaining_active_count > 0)
    RETURNING p.id
)
SELECT
    i.id,
    i.direct_account_count,
    i.default_tenant_count,
    i.group_account_count,
    i.group_remaining_active_count,
    EXISTS (SELECT 1 FROM deleted) AS deleted
FROM impact i;
