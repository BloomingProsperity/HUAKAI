-- Admin Pool Group CRUD (F-POOL-001).

-- name: InsertPool :one
INSERT INTO pool_groups (
    tenant_id,
    name,
    top_k_default,
    capability_default,
    allow_last_resort,
    enabled
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(name)::text,
    sqlc.arg(top_k_default)::integer,
    sqlc.arg(capability_default)::text,
    sqlc.arg(allow_last_resort)::boolean,
    true
)
RETURNING
    id,
    tenant_id,
    name,
    routing_policy_version,
    top_k_default,
    capability_default,
    allow_tenant_operator_force,
    allow_last_resort,
    sticky_wait_max_waiting,
    fallback_wait_max_waiting,
    sticky_wait_timeout_ms,
    fallback_wait_timeout_ms,
    forced_route_rate_limit_per_hour,
    enabled,
    created_at,
    updated_at,
    deleted_at;

-- name: GetPool :one
SELECT
    id,
    tenant_id,
    name,
    routing_policy_version,
    top_k_default,
    capability_default,
    allow_tenant_operator_force,
    allow_last_resort,
    sticky_wait_max_waiting,
    fallback_wait_max_waiting,
    sticky_wait_timeout_ms,
    fallback_wait_timeout_ms,
    forced_route_rate_limit_per_hour,
    enabled,
    created_at,
    updated_at,
    deleted_at
FROM pool_groups
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL;

-- name: ListPools :many
SELECT
    id,
    tenant_id,
    name,
    routing_policy_version,
    top_k_default,
    capability_default,
    allow_tenant_operator_force,
    allow_last_resort,
    sticky_wait_max_waiting,
    fallback_wait_max_waiting,
    sticky_wait_timeout_ms,
    fallback_wait_timeout_ms,
    forced_route_rate_limit_per_hour,
    enabled,
    created_at,
    updated_at,
    deleted_at
FROM pool_groups
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
  AND (
      sqlc.arg(after_id)::bigint = 0
      OR id < sqlc.arg(after_id)::bigint
  )
ORDER BY id DESC
LIMIT sqlc.arg(limit_count)::integer
OFFSET sqlc.arg(page_offset)::integer
;

-- name: UpdatePool :one
UPDATE pool_groups
SET
    name = COALESCE(sqlc.narg(name)::text, name),
    top_k_default = COALESCE(sqlc.narg(top_k_default)::integer, top_k_default),
    capability_default = COALESCE(sqlc.narg(capability_default)::text, capability_default),
    allow_last_resort = COALESCE(sqlc.narg(allow_last_resort)::boolean, allow_last_resort),
    enabled = COALESCE(sqlc.narg(enabled)::boolean, enabled),
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL
RETURNING
    id,
    tenant_id,
    name,
    routing_policy_version,
    top_k_default,
    capability_default,
    allow_tenant_operator_force,
    allow_last_resort,
    sticky_wait_max_waiting,
    fallback_wait_max_waiting,
    sticky_wait_timeout_ms,
    fallback_wait_timeout_ms,
    forced_route_rate_limit_per_hour,
    enabled,
    created_at,
    updated_at,
    deleted_at;

-- name: DeletePool :one
UPDATE pool_groups
SET
    deleted_at = COALESCE(deleted_at, NOW()),
    enabled = false,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL
RETURNING
    id,
    tenant_id,
    name,
    routing_policy_version,
    top_k_default,
    capability_default,
    allow_tenant_operator_force,
    allow_last_resort,
    sticky_wait_max_waiting,
    fallback_wait_max_waiting,
    sticky_wait_timeout_ms,
    fallback_wait_timeout_ms,
    forced_route_rate_limit_per_hour,
    enabled,
    created_at,
    updated_at,
    deleted_at;
