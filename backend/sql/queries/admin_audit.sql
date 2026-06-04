-- Admin audit event queries.
-- These queries are append-only writes. NEVER store
-- plaintext bearer or key_hash inside the payload jsonb.

-- name: InsertAdminAuditEvent :one
-- Append a single admin action audit row. Called inside the same TX as
-- the corresponding api_keys / admin_tokens write so the audit trail is
-- atomic with the action.
INSERT INTO admin_audit_events (
    tenant_id, actor_id, actor_role, action,
    target_type, target_id, request_id, reason, payload
) VALUES (
    sqlc.narg(tenant_id)::bigint,
    sqlc.arg(actor_id)::text,
    sqlc.arg(actor_role)::text,
    sqlc.arg(action)::text,
    sqlc.arg(target_type)::text,
    sqlc.narg(target_id)::bigint,
    sqlc.narg(request_id)::text,
    sqlc.narg(reason)::text,
    sqlc.arg(payload)::jsonb
)
RETURNING id, occurred_at;

-- name: CountIssuanceInWindow :one
-- D4 rate-limit window: how many SUCCESSFUL 'issue_api_key' actions has
-- this actor performed in the last `window_seconds`? Default cap = 30/hour
-- per token, enforced by the issuer service.
--
-- Filter on target_id IS NOT NULL so denied
-- attempts (which write a deny audit row with target_id=0/NULL) are
-- excluded from the cap. Otherwise an actor that hits the cap keeps
-- refreshing the window with deny rows on every retry and never recovers.
SELECT count(*)::bigint
FROM admin_audit_events
WHERE actor_id = sqlc.arg(actor_id)::text
  AND action = 'issue_api_key'
  AND target_id IS NOT NULL
  AND occurred_at > now() - make_interval(secs => sqlc.arg(window_seconds)::integer);

-- name: AcquireAdminBootstrapLock :exec
-- Serialize MaybeBootstrap across concurrently
-- starting gateway instances. Without this lock, two pods that both see
-- empty admin_tokens can each insert a fresh bootstrap row. The lock is
-- a constant key so all instances contend on the same one. Released
-- automatically on TX commit/rollback.
SELECT pg_advisory_xact_lock(hashtextextended('admin_bootstrap'::text, 0));

-- name: AcquireAdminIssuanceLock :exec
-- Serialize per-actor issuance under a
-- transaction-scoped advisory lock so concurrent POST /admin/v1/api-keys
-- from the same admin token cannot race past the 30/hour cap. The lock
-- is keyed on hash(actor_id) and released automatically on TX
-- commit/rollback. Must be called BEFORE CountIssuanceInWindow inside
-- the same TX, otherwise the count→insert window remains racy.
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(actor_id)::text, 0));
