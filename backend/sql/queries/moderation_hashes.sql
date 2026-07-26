-- 内容审核哈希规则查询。

-- name: ListModerationHashes :many
SELECT id, tenant_id, hash_hex, reason_code, enabled, created_at, updated_at
FROM moderation_hashes
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: CreateModerationHash :one
INSERT INTO moderation_hashes (
    tenant_id, hash_hex, reason_code, enabled, created_by, updated_by
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(hash_hex)::text,
    sqlc.arg(reason_code)::text,
    sqlc.arg(enabled)::boolean,
    sqlc.narg(updated_by)::text,
    sqlc.narg(updated_by)::text
)
RETURNING id, tenant_id, hash_hex, reason_code, enabled, created_at, updated_at;

-- name: BulkCreateModerationHashes :many
INSERT INTO moderation_hashes (
    tenant_id, hash_hex, reason_code, enabled, created_by, updated_by
)
SELECT
    sqlc.arg(tenant_id)::bigint,
    h.hash_hex,
    r.reason_code,
    e.enabled,
    sqlc.narg(updated_by)::text,
    sqlc.narg(updated_by)::text
FROM unnest(sqlc.arg(hash_hexes)::text[]) WITH ORDINALITY AS h(hash_hex, ord)
JOIN unnest(sqlc.arg(reason_codes)::text[]) WITH ORDINALITY AS r(reason_code, ord) USING (ord)
JOIN unnest(sqlc.arg(enabled_values)::boolean[]) WITH ORDINALITY AS e(enabled, ord) USING (ord)
ON CONFLICT (tenant_id, hash_hex) WHERE deleted_at IS NULL DO NOTHING
RETURNING id, tenant_id, hash_hex, reason_code, enabled, created_at, updated_at;

-- name: SoftDeleteModerationHash :execrows
UPDATE moderation_hashes
SET enabled = false,
    deleted_at = now(),
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL;

-- name: FindEnabledModerationHash :one
SELECT id, tenant_id, hash_hex, reason_code, enabled, created_at, updated_at
FROM moderation_hashes
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND hash_hex = sqlc.arg(hash_hex)::text
  AND enabled = true
  AND deleted_at IS NULL
LIMIT 1;
