-- Slice 2 (N+4b2) admin-side api_keys queries.
-- Per docs/process/plans/2026-05-01-n4b-admin-keys.md §Scope A.
-- These queries are issued by internal/admin (operator-facing) and are
-- distinct from internal/auth's customer-facing LookupAPIKeysByPrefix:
-- admin tooling MUST NOT use the prefix-only lookup that the customer
-- hot path optimizes for (it's a different security surface).

-- name: AdminInsertAPIKey :one
-- Codex N+4b2 pass-9 P2: insert is conditioned on tenant + user being
-- active and not soft-deleted at the moment of write. INSERT ... SELECT
-- WHERE EXISTS makes "target validity" atomic with the row creation, so
-- a tenant/user that flips disabled between an external preflight and
-- this insert can no longer produce a freshly-minted-but-immediately-
-- rejected key. NoRows return → target became invalid; handler maps it
-- to ErrAdminBadRequest.
INSERT INTO api_keys (
    tenant_id, user_id, name, key_hash, key_prefix, status, expires_at
)
SELECT
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(user_id)::bigint,
    sqlc.arg(name)::text,
    sqlc.arg(key_hash)::text,
    sqlc.arg(key_prefix)::text,
    'active',
    sqlc.narg(expires_at)::timestamptz
WHERE EXISTS (
    SELECT 1 FROM tenants t
    WHERE t.id = sqlc.arg(tenant_id)::bigint
      AND t.deleted_at IS NULL
      AND t.status = 'active'
)
AND EXISTS (
    SELECT 1 FROM users u
    WHERE u.id = sqlc.arg(user_id)::bigint
      AND u.tenant_id = sqlc.arg(tenant_id)::bigint
      AND u.principal_kind = 'human'
      AND u.deleted_at IS NULL
      AND u.status = 'active'
)
RETURNING id, created_at;

-- name: AdminListAPIKeysForTenant :many
-- Lists api_keys metadata for a tenant. NEVER returns key_hash. The
-- key_prefix is acceptable to expose (already public-safe per N+4a; 16
-- chars insufficient to authenticate without bcrypt match).
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
-- Soft-revokes a tenant's api_keys row. Codex N+4b2 pass-6 P2: revoke
-- collapses ANY non-revoked status (active / disabled / expired) into
-- 'revoked' — only an already-revoked row is the idempotent path.
-- Returning 0 rows means "was already revoked".
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
-- Codex N+4b2 pass-5 P2: validate the target (tenant, user) is active
-- and not soft-deleted BEFORE we mint a bearer + bcrypt-hash. Returning
-- false → handler responds 400 (or 404), avoiding "the key was minted but
-- the customer resolver immediately rejects it" + the unhelpful 503 that
-- would result from leaning on the FK as our only validator.
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
-- Tenant-scoped read for revocation flow + audit lookup.
SELECT
    id, tenant_id, user_id, name, key_prefix, status,
    expires_at, last_used_at, revoked_at, revoked_reason,
    created_at, updated_at
FROM api_keys
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND purpose = 'user'
  AND deleted_at IS NULL;
