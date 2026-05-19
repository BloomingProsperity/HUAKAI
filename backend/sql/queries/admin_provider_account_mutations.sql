-- name: InsertProviderAccount :one
INSERT INTO provider_accounts (
    tenant_id,
    provider_id,
    channel_id,
    name,
    account_type,
    enabled,
    expires_at,
    credentials,
    cap_concurrency,
    cap_queue_sticky,
    cap_queue_fallback,
    priority,
    model_allow_list,
    capability_flags,
    created_by_actor,
    last_modified_by_actor
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(provider_id)::bigint,
    sqlc.arg(channel_id)::bigint,
    sqlc.arg(name)::text,
    sqlc.arg(account_type)::text,
    COALESCE(sqlc.narg(enabled)::boolean, true),
    sqlc.narg(expires_at)::timestamptz,
    sqlc.arg(credentials)::jsonb,
    COALESCE(sqlc.narg(cap_concurrency)::integer, 4),
    COALESCE(sqlc.narg(cap_queue_sticky)::integer, 2),
    COALESCE(sqlc.narg(cap_queue_fallback)::integer, 8),
    COALESCE(sqlc.narg(priority)::integer, 100),
    COALESCE(sqlc.narg(model_allow_list)::text[], ARRAY[]::text[]),
    COALESCE(sqlc.narg(capability_flags)::text[], ARRAY[]::text[]),
    sqlc.narg(actor_id)::text,
    sqlc.narg(actor_id)::text
)
RETURNING id;

-- name: UpdateProviderAccountEnabled :exec
UPDATE provider_accounts
SET
    enabled = sqlc.arg(enabled)::boolean,
    updated_at = NOW(),
    last_modified_by_actor = sqlc.narg(actor_id)::text
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;

-- name: SoftDeleteProviderAccount :exec
UPDATE provider_accounts
SET
    deleted_at = COALESCE(deleted_at, NOW()),
    updated_at = NOW(),
    enabled = false,
    last_modified_by_actor = sqlc.narg(actor_id)::text
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;
