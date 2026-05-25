-- Hermes Phase 1 Slice 1 schema gate queries.
-- Scope: settings, API profiles, and append-only audit events only.

-- name: GetSettings :one
SELECT tenant_id, user_id, enabled, api_source, profile_id, created_at, updated_at
FROM hermes_settings
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND user_id = sqlc.arg(user_id)::bigint;

-- name: UpsertSettings :one
INSERT INTO hermes_settings (tenant_id, user_id, enabled, api_source, profile_id)
VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(user_id)::bigint,
    sqlc.arg(enabled)::boolean,
    sqlc.arg(api_source)::text,
    sqlc.narg(profile_id)::bigint
)
ON CONFLICT (tenant_id, user_id)
DO UPDATE SET enabled = EXCLUDED.enabled,
              api_source = EXCLUDED.api_source,
              profile_id = EXCLUDED.profile_id,
              updated_at = NOW()
RETURNING tenant_id, user_id, enabled, api_source, profile_id, created_at, updated_at;

-- name: EnableHermes :one
UPDATE hermes_settings
SET enabled = TRUE,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND user_id = sqlc.arg(user_id)::bigint
RETURNING tenant_id, user_id, enabled, api_source, profile_id, created_at, updated_at;

-- name: DisableHermes :one
UPDATE hermes_settings
SET enabled = FALSE,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND user_id = sqlc.arg(user_id)::bigint
RETURNING tenant_id, user_id, enabled, api_source, profile_id, created_at, updated_at;

-- name: ListProfilesByTenant :many
SELECT id, tenant_id, owner_user_id, name, profile_kind, api_key_id, pool_group_id, created_at, updated_at
FROM hermes_api_profiles
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
ORDER BY created_at DESC, id DESC;

-- name: ListProfilesByOwner :many
SELECT id, tenant_id, owner_user_id, name, profile_kind, api_key_id, pool_group_id, created_at, updated_at
FROM hermes_api_profiles
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND owner_user_id = sqlc.arg(owner_user_id)::bigint
ORDER BY created_at DESC, id DESC;

-- name: CreateProfile :one
INSERT INTO hermes_api_profiles (tenant_id, owner_user_id, name, profile_kind, api_key_id, pool_group_id)
VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(owner_user_id)::bigint,
    sqlc.arg(name)::text,
    sqlc.arg(profile_kind)::text,
    sqlc.narg(api_key_id)::bigint,
    sqlc.narg(pool_group_id)::bigint
)
RETURNING id, tenant_id, owner_user_id, name, profile_kind, api_key_id, pool_group_id, created_at, updated_at;

-- name: UpdateProfile :one
UPDATE hermes_api_profiles
SET name = sqlc.arg(name)::text,
    profile_kind = sqlc.arg(profile_kind)::text,
    api_key_id = sqlc.narg(api_key_id)::bigint,
    pool_group_id = sqlc.narg(pool_group_id)::bigint,
    updated_at = NOW()
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
RETURNING id, tenant_id, owner_user_id, name, profile_kind, api_key_id, pool_group_id, created_at, updated_at;

-- name: DeleteProfile :execrows
DELETE FROM hermes_api_profiles
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: InsertAuditEvent :one
INSERT INTO hermes_audit_events (
    ts, tenant_id, actor_user_id, action,
    sanitized_args, result, correlation_id, request_id
) VALUES (
    sqlc.arg(ts)::timestamptz,
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(actor_user_id)::bigint,
    sqlc.arg(action)::text,
    sqlc.narg(sanitized_args)::jsonb,
    sqlc.arg(result)::text,
    sqlc.narg(correlation_id)::text,
    sqlc.narg(request_id)::text
)
RETURNING id, ts, tenant_id, actor_user_id, action, sanitized_args, result, correlation_id, request_id;

-- name: ListAuditEventsByTenant :many
SELECT id, ts, tenant_id, actor_user_id, action, sanitized_args, result, correlation_id, request_id
FROM hermes_audit_events
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: GetAuditEventByCorrelation :many
SELECT id, ts, tenant_id, actor_user_id, action, sanitized_args, result, correlation_id, request_id
FROM hermes_audit_events
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND correlation_id = sqlc.arg(correlation_id)::text
ORDER BY ts DESC, id DESC;
