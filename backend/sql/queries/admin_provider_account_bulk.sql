-- name: LockProviderAccountForBulk :one
-- 批量运维逐项事务:按 (id, tenant) 锁定未删除账号并读取将改字段的当前值,用于"已达期望态 → skipped"
-- 判定与审计 before/after;他租户或已删除 → no rows(调用方统一映射为 not_found,不区分)。
SELECT id, enabled, priority, static_weight, channel_id, provider_id, account_type, name
FROM provider_accounts
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
FOR UPDATE;

-- name: GetChannelForBulkMove :one
-- 批量改渠道的目标渠道校验:必须属于同一租户且未删除;返回所属池组与启用态供审计与风险提示。
SELECT id, pool_group_id, name, enabled
FROM channels
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;

-- name: UpdateProviderAccountChannel :execrows
-- 批量改渠道:只改 channel_id;调用方已在同一事务内锁定账号、校验目标渠道并完成混合风险判定。
UPDATE provider_accounts
SET channel_id = sqlc.arg(channel_id)::bigint,
    updated_at = now(),
    last_modified_by_actor = sqlc.narg(actor_id)::text
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;
