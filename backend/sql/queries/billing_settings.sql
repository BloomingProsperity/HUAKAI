-- Case C 计费策略租户级设置的增删改查。表见 migration 0046。

-- name: GetBillingSetting :one
-- 按租户和设置键读取单个计费设置。
SELECT id, tenant_id, setting_key, setting_value, updated_at, updated_by
FROM billing_settings
WHERE tenant_id = $1 AND setting_key = $2;

-- name: GetBillingSettingForUpdate :one
-- 事务内按租户和设置键读取并锁住现有计费设置。
SELECT id, tenant_id, setting_key, setting_value, updated_at, updated_by
FROM billing_settings
WHERE tenant_id = $1 AND setting_key = $2
FOR UPDATE;

-- name: AcquireBillingSettingLock :exec
-- 首次写入时目标行还不存在, FOR UPDATE 无法锁住空行; 先拿事务级顾问锁
-- 按租户和设置键串行化同一设置的读改写, 提交或回滚后自动释放。
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(setting_key)::text, sqlc.arg(tenant_id)::bigint));

-- name: UpsertBillingSetting :one
-- 写入或更新单个计费设置; updated_at 总是以数据库时间刷新。
INSERT INTO billing_settings (tenant_id, setting_key, setting_value, updated_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, setting_key)
DO UPDATE SET setting_value = EXCLUDED.setting_value,
              updated_at = now(),
              updated_by = EXCLUDED.updated_by
RETURNING id, tenant_id, setting_key, setting_value, updated_at, updated_by;

-- name: ListBillingSettingsByTenant :many
-- 列出一个租户的全部计费设置。
SELECT id, tenant_id, setting_key, setting_value, updated_at, updated_by
FROM billing_settings
WHERE tenant_id = $1
ORDER BY setting_key;
