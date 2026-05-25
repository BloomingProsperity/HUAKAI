-- name: GetAccountForRefresh :one
SELECT
    id,
    tenant_id,
    provider_id,
    channel_id,
    name,
    account_type,
    enabled,
    expires_at,
    health_state,
    health_state_until,
    credential_state,
    credentials,
    token_version,
    refresh_token_fingerprint,
    last_refresh_at,
    last_refresh_outcome,
    oauth_endpoint_health,
    temp_unschedulable_until,
    temp_unschedulable_reason,
    created_at,
    updated_at,
    deleted_at
FROM provider_accounts
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
FOR UPDATE;

-- name: UpdateAccountCredentialsCAS :execrows
UPDATE provider_accounts
SET
    credentials = sqlc.arg(credentials),
    expires_at = sqlc.arg(expires_at),
    token_version = token_version + 1,
    refresh_token_fingerprint = sqlc.arg(refresh_token_fingerprint),
    last_refresh_at = NOW(),
    last_refresh_outcome = sqlc.arg(last_refresh_outcome),
    updated_at = NOW()
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
  AND token_version = sqlc.arg(token_version);

-- name: MarkAccountTempUnschedulable :exec
UPDATE provider_accounts
SET
    temp_unschedulable_until = sqlc.arg(temp_unschedulable_until),
    temp_unschedulable_reason = sqlc.arg(temp_unschedulable_reason),
    updated_at = NOW()
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id);

-- name: GetTokenVersion :one
SELECT token_version
FROM provider_accounts
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id);
