-- 模型主体运维查询。所有读写都显式携带 scope 与 tenant_id，避免只靠 HTTP 门做归属判断。

-- name: ListAdminModels :many
SELECT
    id,
    tenant_id,
    scope,
    canonical_id,
    protocol_family,
    default_provider_model_id,
    default_context_window,
    default_request_timeout_ms,
    pricing_class,
    model_owner,
    model_created_at,
    capabilities,
    max_output_tokens,
    model_mode,
    status,
    created_at,
    updated_at,
    deleted_at
FROM models
WHERE scope = sqlc.arg(scope)::text
  AND tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::bigint
  AND status <> 'deleted'
  AND deleted_at IS NULL
ORDER BY canonical_id ASC, id ASC;

-- name: GetAdminModel :one
SELECT
    id,
    tenant_id,
    scope,
    canonical_id,
    protocol_family,
    default_provider_model_id,
    default_context_window,
    default_request_timeout_ms,
    pricing_class,
    model_owner,
    model_created_at,
    capabilities,
    max_output_tokens,
    model_mode,
    status,
    created_at,
    updated_at,
    deleted_at
FROM models
WHERE id = sqlc.arg(id)::bigint
  AND scope = sqlc.arg(scope)::text
  AND tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::bigint
  AND status <> 'deleted'
  AND deleted_at IS NULL;

-- name: LockAdminModelForUpdate :one
SELECT
    id,
    tenant_id,
    scope,
    canonical_id,
    protocol_family,
    default_provider_model_id,
    default_context_window,
    default_request_timeout_ms,
    pricing_class,
    model_owner,
    model_created_at,
    capabilities,
    max_output_tokens,
    model_mode,
    status,
    created_at,
    updated_at,
    deleted_at
FROM models
WHERE id = sqlc.arg(id)::bigint
  AND scope = sqlc.arg(scope)::text
  AND tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::bigint
  AND status <> 'deleted'
  AND deleted_at IS NULL
FOR UPDATE;

-- name: CreateAdminModel :one
INSERT INTO models (
    tenant_id,
    scope,
    canonical_id,
    protocol_family,
    default_provider_model_id,
    default_context_window,
    default_request_timeout_ms,
    pricing_class,
    model_owner,
    status
) VALUES (
    sqlc.narg(tenant_id)::bigint,
    sqlc.arg(scope)::text,
    sqlc.arg(canonical_id)::text,
    sqlc.arg(protocol_family)::text,
    sqlc.arg(default_provider_model_id)::text,
    sqlc.arg(default_context_window)::integer,
    sqlc.arg(default_request_timeout_ms)::integer,
    sqlc.arg(pricing_class)::text,
    sqlc.arg(model_owner)::text,
    sqlc.arg(status)::text
)
RETURNING
    id,
    tenant_id,
    scope,
    canonical_id,
    protocol_family,
    default_provider_model_id,
    default_context_window,
    default_request_timeout_ms,
    pricing_class,
    model_owner,
    model_created_at,
    capabilities,
    max_output_tokens,
    model_mode,
    status,
    created_at,
    updated_at,
    deleted_at;

-- name: UpdateAdminModel :one
UPDATE models
SET default_provider_model_id = sqlc.arg(default_provider_model_id)::text,
    default_context_window = sqlc.arg(default_context_window)::integer,
    default_request_timeout_ms = sqlc.arg(default_request_timeout_ms)::integer,
    pricing_class = sqlc.arg(pricing_class)::text,
    protocol_family = sqlc.arg(protocol_family)::text,
    model_owner = sqlc.arg(model_owner)::text,
    status = sqlc.arg(status)::text,
    updated_at = now()
WHERE id = sqlc.arg(id)::bigint
  AND scope = sqlc.arg(scope)::text
  AND tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::bigint
  AND status <> 'deleted'
  AND deleted_at IS NULL
RETURNING
    id,
    tenant_id,
    scope,
    canonical_id,
    protocol_family,
    default_provider_model_id,
    default_context_window,
    default_request_timeout_ms,
    pricing_class,
    model_owner,
    model_created_at,
    capabilities,
    max_output_tokens,
    model_mode,
    status,
    created_at,
    updated_at,
    deleted_at;

-- name: SoftDeleteAdminModel :one
UPDATE models
SET status = 'deleted',
    deleted_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)::bigint
  AND scope = sqlc.arg(scope)::text
  AND tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::bigint
  AND status <> 'deleted'
  AND deleted_at IS NULL
RETURNING
    id,
    tenant_id,
    scope,
    canonical_id,
    protocol_family,
    default_provider_model_id,
    default_context_window,
    default_request_timeout_ms,
    pricing_class,
    model_owner,
    model_created_at,
    capabilities,
    max_output_tokens,
    model_mode,
    status,
    created_at,
    updated_at,
    deleted_at;
