-- Hermes 设置、模型档案与日志查询。

-- name: GetSettings :one
SELECT tenant_id, user_id, enabled, api_source, profile_id, created_at, updated_at, model_key
FROM hermes_settings
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND user_id = sqlc.arg(user_id)::bigint;

-- name: UpsertSettings :one
INSERT INTO hermes_settings (tenant_id, user_id, enabled, api_source, profile_id, model_key)
VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(user_id)::bigint,
    sqlc.arg(enabled)::boolean,
    sqlc.arg(api_source)::text,
    sqlc.narg(profile_id)::bigint,
    sqlc.arg(model_key)::text
)
ON CONFLICT (tenant_id, user_id)
DO UPDATE SET enabled = EXCLUDED.enabled,
              api_source = EXCLUDED.api_source,
              profile_id = EXCLUDED.profile_id,
              model_key = EXCLUDED.model_key,
              updated_at = NOW()
RETURNING tenant_id, user_id, enabled, api_source, profile_id, created_at, updated_at, model_key;

-- name: EnableHermes :one
UPDATE hermes_settings
SET enabled = TRUE,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND user_id = sqlc.arg(user_id)::bigint
RETURNING tenant_id, user_id, enabled, api_source, profile_id, created_at, updated_at, model_key;

-- name: DisableHermes :one
UPDATE hermes_settings
SET enabled = FALSE,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND user_id = sqlc.arg(user_id)::bigint
RETURNING tenant_id, user_id, enabled, api_source, profile_id, created_at, updated_at, model_key;

-- name: ListProfilesByTenant :many
SELECT *
FROM hermes_api_profiles
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
ORDER BY created_at DESC, id DESC;

-- name: ListProfilesByOwner :many
SELECT *
FROM hermes_api_profiles
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND owner_user_id = sqlc.arg(owner_user_id)::bigint
ORDER BY created_at DESC, id DESC;

-- name: GetProfile :one
SELECT *
FROM hermes_api_profiles
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: CreateProfile :one
INSERT INTO hermes_api_profiles (
    tenant_id, owner_user_id, name, profile_kind, base_url,
    encrypted_api_key, encryption_scheme, key_id, nonce, aad_hash,
    api_key_fingerprint, api_key_hint, credential_version, secret_binding_id
)
VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(owner_user_id)::bigint,
    sqlc.arg(name)::text,
    sqlc.arg(profile_kind)::text,
    sqlc.arg(base_url)::text,
    sqlc.arg(encrypted_api_key)::bytea,
    sqlc.arg(encryption_scheme)::text,
    sqlc.arg(key_id)::text,
    sqlc.arg(nonce)::bytea,
    sqlc.arg(aad_hash)::text,
    sqlc.arg(api_key_fingerprint)::text,
    sqlc.arg(api_key_hint)::text,
    sqlc.arg(credential_version)::integer,
    sqlc.arg(secret_binding_id)::bigint
)
RETURNING *;

-- name: RotateProfileCredential :one
UPDATE hermes_api_profiles
SET name = sqlc.arg(name)::text,
    base_url = sqlc.arg(base_url)::text,
    encrypted_api_key = sqlc.arg(encrypted_api_key)::bytea,
    encryption_scheme = sqlc.arg(encryption_scheme)::text,
    key_id = sqlc.arg(key_id)::text,
    nonce = sqlc.arg(nonce)::bytea,
    aad_hash = sqlc.arg(aad_hash)::text,
    api_key_fingerprint = sqlc.arg(api_key_fingerprint)::text,
    api_key_hint = sqlc.arg(api_key_hint)::text,
    credential_version = sqlc.arg(new_credential_version)::integer,
    updated_at = NOW()
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND credential_version = sqlc.arg(expected_credential_version)::integer
RETURNING *;

-- name: DeleteProfile :execrows
DELETE FROM hermes_api_profiles
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: ProfileInUse :one
SELECT EXISTS (
    SELECT 1
    FROM hermes_settings
    WHERE tenant_id = sqlc.arg(tenant_id)::bigint
      AND profile_id = sqlc.arg(profile_id)::bigint
    LIMIT 1
)::boolean;

-- name: InsertAuditEvent :one
INSERT INTO hermes_audit_events (
    ts, tenant_id, actor_source, actor_id, actor_role, action,
    sanitized_args, result, correlation_id, request_id, log_category
) VALUES (
    sqlc.arg(ts)::timestamptz,
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(actor_source)::text,
    sqlc.arg(actor_id)::bigint,
    sqlc.arg(actor_role)::text,
    sqlc.arg(action)::text,
    sqlc.narg(sanitized_args)::jsonb,
    sqlc.arg(result)::text,
    sqlc.narg(correlation_id)::text,
    sqlc.narg(request_id)::text,
    sqlc.arg(log_category)::text
)
RETURNING *;

-- name: ListAuditEventsByTenant :many
SELECT id, ts, tenant_id, actor_source, actor_id, actor_role, action,
       sanitized_args, result, correlation_id, request_id, log_category
FROM hermes_audit_events
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: GetAuditEventByCorrelation :many
SELECT id, ts, tenant_id, actor_source, actor_id, actor_role, action,
       sanitized_args, result, correlation_id, request_id, log_category
FROM hermes_audit_events
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND correlation_id = sqlc.arg(correlation_id)::text
ORDER BY ts DESC, id DESC;
