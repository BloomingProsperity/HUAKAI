-- 内容审核规则、哈希与租户配置查询。

-- name: ListEnabledModerationKeywords :many
SELECT id, tenant_id, keyword, reason_code, enabled, created_at, updated_at
FROM moderation_keywords
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND enabled = true
  AND deleted_at IS NULL
ORDER BY id ASC;

-- name: ListModerationKeywords :many
SELECT id, tenant_id, keyword, reason_code, enabled, created_at, updated_at
FROM moderation_keywords
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: CreateModerationKeyword :one
INSERT INTO moderation_keywords (
    tenant_id, keyword, reason_code, enabled, created_by, updated_by
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(keyword)::text,
    sqlc.arg(reason_code)::text,
    sqlc.arg(enabled)::boolean,
    sqlc.narg(updated_by)::text,
    sqlc.narg(updated_by)::text
)
RETURNING id, tenant_id, keyword, reason_code, enabled, created_at, updated_at;

-- name: BulkCreateModerationKeywords :many
INSERT INTO moderation_keywords (
    tenant_id, keyword, reason_code, enabled, created_by, updated_by
)
SELECT
    sqlc.arg(tenant_id)::bigint,
    k.keyword,
    r.reason_code,
    e.enabled,
    sqlc.narg(updated_by)::text,
    sqlc.narg(updated_by)::text
FROM unnest(sqlc.arg(keywords)::text[]) WITH ORDINALITY AS k(keyword, ord)
JOIN unnest(sqlc.arg(reason_codes)::text[]) WITH ORDINALITY AS r(reason_code, ord) USING (ord)
JOIN unnest(sqlc.arg(enabled_values)::boolean[]) WITH ORDINALITY AS e(enabled, ord) USING (ord)
ON CONFLICT (tenant_id, lower(keyword)) WHERE deleted_at IS NULL DO NOTHING
RETURNING id, tenant_id, keyword, reason_code, enabled, created_at, updated_at;

-- name: SoftDeleteModerationKeyword :execrows
UPDATE moderation_keywords
SET enabled = false,
    deleted_at = now(),
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL;

-- name: GetModerationKeyword :one
-- 供 Hermes mutating 工具 moderation_keyword_enable/disable 的 Resolve 按租户+id 读取单条
-- 未软删关键词(复检租户归属 + 渲染预览)。
SELECT id, tenant_id, keyword, reason_code, enabled, created_at, updated_at
FROM moderation_keywords
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL;

-- name: SetModerationKeywordEnabled :execrows
-- 供 Hermes mutating 工具在 orchestrator 事务内定向翻转单条未软删关键词的 enabled 列
-- (只改 enabled + updated_at;租户 scope 绑死在 WHERE tenant_id)。
UPDATE moderation_keywords
SET enabled = sqlc.arg(enabled)::boolean,
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL;

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

-- name: GetModerationConfig :one
SELECT tenant_id, enabled, fail_closed, sample_rate_pct, ban_threshold,
       ban_window_seconds, violation_fee_usd, updated_by, updated_at
FROM moderation_config
WHERE tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: UpsertModerationConfig :one
INSERT INTO moderation_config (
    tenant_id, enabled, fail_closed, sample_rate_pct,
    ban_threshold, ban_window_seconds, violation_fee_usd, updated_by
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(enabled)::boolean,
    sqlc.arg(fail_closed)::boolean,
    sqlc.arg(sample_rate_pct)::integer,
    sqlc.arg(ban_threshold)::integer,
    sqlc.arg(ban_window_seconds)::integer,
    sqlc.arg(violation_fee_usd)::numeric,
    sqlc.narg(updated_by)::text
)
ON CONFLICT (tenant_id) DO UPDATE
SET enabled = EXCLUDED.enabled,
    fail_closed = EXCLUDED.fail_closed,
    sample_rate_pct = EXCLUDED.sample_rate_pct,
    ban_threshold = EXCLUDED.ban_threshold,
    ban_window_seconds = EXCLUDED.ban_window_seconds,
    violation_fee_usd = EXCLUDED.violation_fee_usd,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING tenant_id, enabled, fail_closed, sample_rate_pct, ban_threshold,
          ban_window_seconds, violation_fee_usd, updated_by, updated_at;
