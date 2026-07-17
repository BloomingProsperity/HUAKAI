-- 渠道目录写操作。三个请求改写字段均在 channels 上维护。

-- name: CreateChannel :one
-- pool_group 必须属于同租户(EXISTS 守卫防跨租户链接);name 唯一冲突由
-- uq_channels_tenant_pool_name 抛 23505。
INSERT INTO channels (
    tenant_id,
    pool_group_id,
    name,
    failover_status_codes,
    body_param_strips,
    param_override,
    sensitive_words,
    enabled
)
SELECT
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(pool_group_id)::bigint,
    sqlc.arg(name)::text,
    sqlc.arg(failover_status_codes)::integer[],
    COALESCE(sqlc.narg(body_param_strips)::text[], ARRAY[]::text[]),
    COALESCE(sqlc.narg(param_override)::jsonb, '{}'::jsonb),
    COALESCE(sqlc.narg(sensitive_words)::text[], ARRAY[]::text[]),
    sqlc.arg(enabled)::boolean
WHERE EXISTS (
    SELECT 1 FROM pool_groups pg
    WHERE pg.id = sqlc.arg(pool_group_id)::bigint
      AND pg.tenant_id = sqlc.arg(tenant_id)::bigint
)
RETURNING id, pool_group_id, name, failover_status_codes,
          body_param_strips, param_override, sensitive_words, enabled, created_at;

-- name: UpdateChannel :one
UPDATE channels c
SET
    pool_group_id = sqlc.arg(pool_group_id)::bigint,
    name = sqlc.arg(name)::text,
    failover_status_codes = sqlc.arg(failover_status_codes)::integer[],
    body_param_strips = CASE
        WHEN sqlc.arg(set_body_param_strips)::boolean
        THEN COALESCE(sqlc.narg(body_param_strips)::text[], ARRAY[]::text[])
        ELSE c.body_param_strips
    END,
    param_override = CASE
        WHEN sqlc.arg(set_param_override)::boolean
        THEN COALESCE(sqlc.narg(param_override)::jsonb, '{}'::jsonb)
        ELSE c.param_override
    END,
    sensitive_words = CASE
        WHEN sqlc.arg(set_sensitive_words)::boolean
        THEN COALESCE(sqlc.narg(sensitive_words)::text[], ARRAY[]::text[])
        ELSE c.sensitive_words
    END,
    enabled = sqlc.arg(enabled)::boolean,
    updated_at = NOW()
WHERE c.tenant_id = sqlc.arg(tenant_id)::bigint
  AND c.id = sqlc.arg(id)::bigint
  AND c.deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM pool_groups pg
      WHERE pg.id = sqlc.arg(pool_group_id)::bigint
        AND pg.tenant_id = sqlc.arg(tenant_id)::bigint
  )
RETURNING id, pool_group_id, name, failover_status_codes,
          body_param_strips, param_override, sensitive_words, enabled, created_at;

-- name: SoftDeleteChannel :one
UPDATE channels c
SET
    deleted_at = COALESCE(c.deleted_at, NOW()),
    updated_at = NOW(),
    enabled = false
WHERE c.tenant_id = sqlc.arg(tenant_id)::bigint
  AND c.id = sqlc.arg(id)::bigint
  AND c.deleted_at IS NULL
RETURNING id, pool_group_id, name, failover_status_codes, enabled, created_at;
