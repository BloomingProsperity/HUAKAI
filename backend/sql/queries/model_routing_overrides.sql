-- 模型路由强制 pin 的租户域管理查询；消费查询仍由 pool_accounts.sql 负责。

-- name: CreateModelRoutingOverrideAdmin :one
INSERT INTO model_routing_overrides (
    tenant_id,
    pool_group_id,
    model,
    provider_account_ids,
    enabled
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(pool_group_id)::bigint,
    sqlc.arg(model)::text,
    sqlc.arg(provider_account_ids)::bigint[],
    sqlc.arg(enabled)::boolean
)
RETURNING
    id,
    tenant_id,
    pool_group_id,
    model,
    provider_account_ids,
    enabled,
    created_at,
    updated_at,
    deleted_at;

-- name: GetModelRoutingOverrideAdminForUpdate :one
SELECT
    id,
    tenant_id,
    pool_group_id,
    model,
    provider_account_ids,
    enabled,
    created_at,
    updated_at,
    deleted_at
FROM model_routing_overrides
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL
FOR UPDATE;

-- name: ListModelRoutingOverridesAdmin :many
SELECT
    id,
    tenant_id,
    pool_group_id,
    model,
    provider_account_ids,
    enabled,
    created_at,
    updated_at,
    deleted_at
FROM model_routing_overrides
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
ORDER BY updated_at DESC, id DESC;

-- name: LockModelRoutingPoolForTenant :one
SELECT id
FROM pool_groups
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL
FOR KEY SHARE;

-- name: LockModelRoutingAccountsForPool :many
SELECT pa.id
FROM provider_accounts pa
JOIN channels c
  ON c.id = pa.channel_id
 AND c.tenant_id = pa.tenant_id
 AND c.deleted_at IS NULL
WHERE pa.tenant_id = sqlc.arg(tenant_id)::bigint
  AND c.pool_group_id = sqlc.arg(pool_group_id)::bigint
  AND pa.id = ANY(sqlc.arg(provider_account_ids)::bigint[])
  AND pa.deleted_at IS NULL
FOR KEY SHARE OF pa, c;

-- name: UpdateModelRoutingOverrideAdmin :one
UPDATE model_routing_overrides
SET
    provider_account_ids = sqlc.arg(provider_account_ids)::bigint[],
    enabled = sqlc.arg(enabled)::boolean,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL
RETURNING
    id,
    tenant_id,
    pool_group_id,
    model,
    provider_account_ids,
    enabled,
    created_at,
    updated_at,
    deleted_at;

-- name: DeleteModelRoutingOverrideAdmin :one
UPDATE model_routing_overrides
SET
    enabled = false,
    deleted_at = NOW(),
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL
RETURNING
    id,
    tenant_id,
    pool_group_id,
    model,
    provider_account_ids,
    enabled,
    created_at,
    updated_at,
    deleted_at;
