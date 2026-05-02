-- Slice 2 (N+4b2) admin_tokens queries.
-- Per docs/plans/2026-05-01-n4b-admin-keys.md §Scope A.
-- Per CMB-1: this file is consumed only by internal/admin and never by
-- internal/auth (the inbound customer resolver). Per CMB-5: queries
-- never SELECT key_hash for any purpose other than bcrypt comparison
-- inside the resolver, and never join into log/trace fields.

-- name: LookupAdminTokenByPrefix :many
-- Returns active candidates whose key_prefix matches. Mirrors the
-- LookupAPIKeysByPrefix shape from N+4a (LIMIT 5 caps bcrypt fanout DOS).
-- Status is enforced here; deleted_at filters parent rows.
--
-- Codex N+4b1 pass-3 P2-A fix: tenant_operator tokens MUST be rejected
-- when their scoped tenant is disabled or soft-deleted. We LEFT JOIN
-- tenants so platform_admin tokens (scope_tenant_id IS NULL) still
-- resolve, but tenant_operator tokens whose tenant is disabled/deleted
-- get filtered at the SQL layer — no app-side check required.
SELECT
    at.id,
    at.name,
    at.key_hash,
    at.role,
    at.scope_tenant_id,
    at.bootstrap,
    at.status,
    at.expires_at
FROM admin_tokens at
LEFT JOIN tenants t
    ON at.scope_tenant_id IS NOT NULL
    AND t.id = at.scope_tenant_id
WHERE at.key_prefix = sqlc.arg(key_prefix)::text
  AND at.deleted_at IS NULL
  AND at.status = 'active'
  AND (
        at.scope_tenant_id IS NULL              -- platform_admin: no tenant scope
        OR (t.id IS NOT NULL AND t.deleted_at IS NULL AND t.status = 'active')
      )
ORDER BY at.id
LIMIT 5;

-- name: InsertAdminToken :one
-- Creates an admin_tokens row. Caller passes a pre-bcrypt-hashed key_hash
-- and the prefix derived from the plaintext (first 16 chars).
INSERT INTO admin_tokens (
    name, key_hash, key_prefix, role, scope_tenant_id, bootstrap, expires_at
) VALUES (
    sqlc.arg(name)::text,
    sqlc.arg(key_hash)::text,
    sqlc.arg(key_prefix)::text,
    sqlc.arg(role)::text,
    sqlc.narg(scope_tenant_id)::bigint,
    sqlc.arg(bootstrap)::boolean,
    sqlc.narg(expires_at)::timestamptz
)
RETURNING id;

-- name: CountAdminTokensIncludingInactive :one
-- Used by bootstrap: env-var bootstrap MUST only insert when NO
-- admin token row has ever been minted (regardless of current status).
-- Codex N+4b2 pass-5 P1: an active-only count would let a stale env
-- var re-bootstrap after the operator disabled/revoked all tokens, which
-- breaks the "one-shot" guarantee. Counting all non-deleted rows closes
-- that hole; if you want to wipe and re-bootstrap, hard-delete the row.
SELECT count(*)
FROM admin_tokens
WHERE deleted_at IS NULL;

-- name: DisableBootstrapAdminTokens :execrows
-- After the operator issues a real (non-bootstrap) admin token, the
-- bootstrap rows should be auto-disabled so the env-var token is no
-- longer accepted by the resolver. Idempotent.
UPDATE admin_tokens
SET status = 'disabled', updated_at = NOW()
WHERE bootstrap = true AND status = 'active';

-- name: RevokeAdminToken :execrows
-- Soft-revoke an admin token. Tenant-bound revocation isn't enforced here
-- because admin_tokens has no tenant ownership for platform_admin rows;
-- the handler-side RBAC check decides whether the caller can revoke.
UPDATE admin_tokens
SET status = 'revoked',
    revoked_at = NOW(),
    revoked_reason = sqlc.arg(reason)::text,
    updated_at = NOW()
WHERE id = sqlc.arg(id)::bigint AND status = 'active';
