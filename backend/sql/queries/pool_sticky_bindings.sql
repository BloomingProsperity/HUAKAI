-- name: GetStickyBinding :one
SELECT provider_account_id
FROM sticky_bindings
WHERE tenant_id = sqlc.arg(tenant_id)
  AND session_hash = sqlc.arg(session_hash)
  AND model = sqlc.arg(model)
  AND expires_at > NOW();

-- name: UpsertStickyBinding :exec
INSERT INTO sticky_bindings (
    tenant_id,
    session_hash,
    model,
    provider_account_id,
    expires_at
) VALUES (
    sqlc.arg(tenant_id),
    sqlc.arg(session_hash),
    sqlc.arg(model),
    sqlc.arg(provider_account_id),
    sqlc.arg(expires_at)
)
ON CONFLICT (tenant_id, session_hash, model)
DO UPDATE SET
    provider_account_id = EXCLUDED.provider_account_id,
    expires_at = EXCLUDED.expires_at,
    refreshed_at = NOW();

-- name: DeleteExpiredStickyBindings :exec
DELETE FROM sticky_bindings
WHERE expires_at < NOW();
