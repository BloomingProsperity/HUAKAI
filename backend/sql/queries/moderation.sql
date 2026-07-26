-- 内容审核关键词规则查询。

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
