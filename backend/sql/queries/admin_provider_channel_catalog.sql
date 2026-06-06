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

-- name: InsertProvider :one
INSERT INTO providers (
    tenant_id,
    code,
    display_name,
    upstream_protocol,
    enabled
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(code)::text,
    sqlc.arg(display_name)::text,
    sqlc.arg(upstream_protocol)::text,
    sqlc.arg(enabled)::boolean
)
RETURNING id, code, display_name, upstream_protocol, enabled, created_at;

-- name: UpdateProvider :one
UPDATE providers
SET
    display_name = sqlc.arg(display_name)::text,
    upstream_protocol = sqlc.arg(upstream_protocol)::text,
    enabled = sqlc.arg(enabled)::boolean,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND code = sqlc.arg(code)::text
  AND deleted_at IS NULL
RETURNING id, code, display_name, upstream_protocol, enabled, created_at;

-- name: CountActiveProviderAccountsForProvider :one
SELECT count(*)::bigint
FROM provider_accounts pa
INNER JOIN providers p
    ON p.id = pa.provider_id
   AND p.tenant_id = pa.tenant_id
WHERE p.tenant_id = sqlc.arg(tenant_id)::bigint
  AND p.code = sqlc.arg(code)::text
  AND p.deleted_at IS NULL
  AND pa.deleted_at IS NULL
  AND pa.enabled = true;

-- name: SoftDeleteProvider :one
UPDATE providers p
SET
    deleted_at = COALESCE(p.deleted_at, NOW()),
    updated_at = NOW(),
    enabled = false
WHERE p.tenant_id = sqlc.arg(tenant_id)::bigint
  AND p.code = sqlc.arg(code)::text
  AND p.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM provider_accounts pa
      WHERE pa.tenant_id = p.tenant_id
        AND pa.provider_id = p.id
        AND pa.deleted_at IS NULL
        AND pa.enabled = true
  )
RETURNING id, code, display_name, upstream_protocol, enabled, created_at;

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
