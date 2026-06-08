-- name: GetAdminProviderAccountHealth :one
SELECT
    pa.id,
    pa.tenant_id,
    pa.health_state,
    pa.health_state_until,
    pa.enabled,
    pa.last_probe_latency_ms,
    pa.last_probe_at,
    COALESCE(ac.last_refresh_at, pa.last_refresh_at) AS last_refresh_at,
    COALESCE(ac.last_refresh_outcome, pa.last_refresh_outcome) AS last_refresh_outcome,
    ac.failure_class,
    COALESCE(ac.failure_count, 0)::integer AS failure_count,
    pa.updated_at
FROM provider_accounts pa
LEFT JOIN LATERAL (
    SELECT
        last_refresh_at,
        last_refresh_outcome,
        failure_class,
        failure_count
    FROM account_credentials ac
    WHERE ac.tenant_id = pa.tenant_id
      AND ac.provider_account_id = pa.id
      AND ac.deleted_at IS NULL
      AND ac.state NOT IN ('revoked')
    ORDER BY ac.last_refresh_at DESC NULLS LAST, ac.updated_at DESC, ac.credential_version DESC, ac.id DESC
    LIMIT 1
) ac ON true
WHERE pa.tenant_id = sqlc.arg(tenant_id)::bigint
  AND pa.id = sqlc.arg(id)::bigint
  AND pa.deleted_at IS NULL;
