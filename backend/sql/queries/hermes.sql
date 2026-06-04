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

-- name: GetProfile :one
SELECT id, tenant_id, owner_user_id, name, profile_kind, api_key_id, pool_group_id, created_at, updated_at
FROM hermes_api_profiles
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: GetAPIKeyOwner :one
SELECT user_id
FROM api_keys
WHERE id = sqlc.arg(api_key_id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;

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

-- Hermes Phase 1 Slice 2 schema gate queries.
-- Scope: conversations, messages, and JWT public key registry.

-- name: CreateConversation :one
INSERT INTO hermes_conversations (tenant_id, owner_user_id, title)
VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(owner_user_id)::bigint,
    sqlc.narg(title)::text
)
RETURNING id;

-- name: GetConversation :one
SELECT id, tenant_id, owner_user_id, title, created_at, updated_at, last_message_at, deleted_at
FROM hermes_conversations
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: ListConversationsByOwner :many
SELECT id, tenant_id, owner_user_id, title, created_at, updated_at, last_message_at, deleted_at
FROM hermes_conversations
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND owner_user_id = sqlc.arg(owner_user_id)::bigint
  AND deleted_at IS NULL
ORDER BY last_message_at DESC NULLS LAST, updated_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: SoftDeleteConversation :execrows
UPDATE hermes_conversations
SET deleted_at = COALESCE(deleted_at, NOW()),
    updated_at = NOW()
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: UpdateConversationLastMessageAt :execrows
UPDATE hermes_conversations
SET last_message_at = sqlc.arg(ts)::timestamptz,
    updated_at = NOW()
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;

-- name: AppendMessage :one
INSERT INTO hermes_messages (tenant_id, conversation_id, role, content, content_ciphertext, token_count, completed_at)
SELECT
    c.tenant_id,
    c.id,
    sqlc.arg(role)::text,
    sqlc.arg(content)::jsonb,
    sqlc.narg(content_ciphertext)::bytea,
    sqlc.narg(token_count)::integer,
    sqlc.narg(completed_at)::timestamptz
FROM hermes_conversations c
WHERE c.id = sqlc.arg(conversation_id)::bigint
  AND c.tenant_id = sqlc.arg(tenant_id)::bigint
  AND c.deleted_at IS NULL
RETURNING id;

-- name: ListMessagesByConversation :many
SELECT m.id, m.tenant_id, m.conversation_id, m.role, m.content, m.content_ciphertext,
       m.token_count, m.completed_at, m.created_at
FROM hermes_messages m
INNER JOIN hermes_conversations c
    ON c.tenant_id = m.tenant_id
    AND c.id = m.conversation_id
    AND c.deleted_at IS NULL
WHERE m.tenant_id = sqlc.arg(tenant_id)::bigint
  AND m.conversation_id = sqlc.arg(conversation_id)::bigint
  AND c.owner_user_id = sqlc.arg(owner_user_id)::bigint
ORDER BY m.created_at ASC, m.id ASC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: PurgeMessagesBefore :execrows
WITH victims AS (
    SELECT id
    FROM hermes_messages
    WHERE created_at < sqlc.arg(cutoff)::timestamptz
    ORDER BY created_at ASC, id ASC
    LIMIT sqlc.arg(batch_limit)::integer
)
DELETE FROM hermes_messages
WHERE id IN (SELECT id FROM victims)
RETURNING id;

-- name: UpdateMessageCompleted :execrows
UPDATE hermes_messages
SET token_count = sqlc.narg(token_count)::integer,
    completed_at = sqlc.arg(completed_at)::timestamptz
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: InsertJWTKey :one
INSERT INTO hermes_jwt_keys (kid, alg, public_key_pem, valid_until)
VALUES (
    sqlc.arg(kid)::text,
    sqlc.arg(alg)::text,
    sqlc.arg(public_key_pem)::text,
    sqlc.narg(valid_until)::timestamptz
)
RETURNING kid, alg, public_key_pem, valid_from, valid_until, revoked_at, created_at;

-- name: GetActiveJWTKeys :many
SELECT kid, alg, public_key_pem, valid_from, valid_until, revoked_at, created_at
FROM hermes_jwt_keys
WHERE valid_from <= NOW()
  AND (valid_until IS NULL OR valid_until > NOW())
  AND revoked_at IS NULL
ORDER BY valid_from DESC, kid ASC;

-- name: GetJWTKeyByKid :one
SELECT kid, alg, public_key_pem, valid_from, valid_until, revoked_at, created_at
FROM hermes_jwt_keys
WHERE kid = sqlc.arg(kid)::text;

-- name: RevokeJWTKey :execrows
UPDATE hermes_jwt_keys
SET revoked_at = NOW()
WHERE kid = sqlc.arg(kid)::text
  AND revoked_at IS NULL
RETURNING kid;
