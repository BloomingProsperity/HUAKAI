-- P0 provider/channel admin catalog queries.
-- Read-only directory data for admin UI. These SELECT lists intentionally
-- exclude tenant_id and every credential-bearing provider_accounts column.

-- name: ListAdminProvidersByTenant :many
SELECT
    id,
    code,
    display_name,
    upstream_protocol,
    enabled,
    created_at
FROM providers
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
ORDER BY code ASC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: ListAdminChannelsByTenant :many
SELECT
    id,
    pool_group_id,
    name,
    failover_status_codes,
    enabled,
    created_at
FROM channels
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
ORDER BY pool_group_id, name
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;
