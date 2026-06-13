-- HUAKAI 配额策略管理面 (BILL-122 /admin/v1/quota-policies) sqlc 查询。
-- 与 reserve-path 读 (quota.sql) 分离: 这些查询给运营后台 CRUD 用, 故意不过滤
-- enabled/valid_until/mode, 让运营看到禁用+过期+影子策略全集; 也单独成包让
-- 生成文件按职责拆分, 不撑大 reserve-path 的 quota.sql.go。
-- 约束: 所有读写定位都显式带 tenant_id, 防跨租户误读/误改。

-- name: InsertQuotaPolicy :one
-- 建一条配额策略并回写全列; live unique / valid_range / >=0 CHECK 由库守。
INSERT INTO quota_policies (
    tenant_id, scope_kind, scope_id, metric, window_kind, window_seconds,
    limit_value, burst_value, mode, priority, enabled,
    valid_from, valid_until, created_by_actor, last_modified_by_actor
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(scope_kind)::text,
    sqlc.arg(scope_id)::text,
    sqlc.arg(metric)::text,
    sqlc.arg(window_kind)::text,
    sqlc.arg(window_seconds)::integer,
    sqlc.arg(limit_value)::numeric(20,8),
    sqlc.arg(burst_value)::numeric(20,8),
    sqlc.arg(mode)::text,
    sqlc.arg(priority)::integer,
    sqlc.arg(enabled)::boolean,
    sqlc.arg(valid_from)::timestamptz,
    sqlc.narg(valid_until)::timestamptz,
    sqlc.narg(created_by_actor)::text,
    sqlc.narg(last_modified_by_actor)::text
)
RETURNING
    tenant_id, id, scope_kind, scope_id, metric, window_kind, window_seconds,
    limit_value, burst_value, mode, priority, enabled,
    valid_from, valid_until, created_by_actor, last_modified_by_actor,
    created_at, updated_at;

-- name: UpdateQuotaPolicy :one
-- 整行覆盖一条策略 (含 enabled/mode 开关), 仍按 tenant_id+id 定位防越租户。
UPDATE quota_policies
SET scope_kind = sqlc.arg(scope_kind)::text,
    scope_id = sqlc.arg(scope_id)::text,
    metric = sqlc.arg(metric)::text,
    window_kind = sqlc.arg(window_kind)::text,
    window_seconds = sqlc.arg(window_seconds)::integer,
    limit_value = sqlc.arg(limit_value)::numeric(20,8),
    burst_value = sqlc.arg(burst_value)::numeric(20,8),
    mode = sqlc.arg(mode)::text,
    priority = sqlc.arg(priority)::integer,
    enabled = sqlc.arg(enabled)::boolean,
    valid_from = sqlc.arg(valid_from)::timestamptz,
    valid_until = sqlc.narg(valid_until)::timestamptz,
    last_modified_by_actor = sqlc.narg(last_modified_by_actor)::text,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
RETURNING
    tenant_id, id, scope_kind, scope_id, metric, window_kind, window_seconds,
    limit_value, burst_value, mode, priority, enabled,
    valid_from, valid_until, created_by_actor, last_modified_by_actor,
    created_at, updated_at;

-- name: GetQuotaPolicyByID :one
-- 单行读, tenant_id 与 id 双定位; 缺行 -> sqlc 返回 ErrNoRows -> handler 404。
SELECT
    tenant_id, id, scope_kind, scope_id, metric, window_kind, window_seconds,
    limit_value, burst_value, mode, priority, enabled,
    valid_from, valid_until, created_by_actor, last_modified_by_actor,
    created_at, updated_at
FROM quota_policies
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint;

-- name: ListQuotaPoliciesForAdmin :many
-- 运营后台列表: 可选 scope_kind/scope_id/metric/enabled 过滤 + 分页。
-- 故意不过滤 valid_from/valid_until/mode, 运营须看到过期+影子+禁用全集。
SELECT
    tenant_id, id, scope_kind, scope_id, metric, window_kind, window_seconds,
    limit_value, burst_value, mode, priority, enabled,
    valid_from, valid_until, created_by_actor, last_modified_by_actor,
    created_at, updated_at
FROM quota_policies
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND (sqlc.narg(scope_kind)::text IS NULL OR scope_kind = sqlc.narg(scope_kind)::text)
  AND (sqlc.narg(scope_id)::text IS NULL OR scope_id = sqlc.narg(scope_id)::text)
  AND (sqlc.narg(metric)::text IS NULL OR metric = sqlc.narg(metric)::text)
  AND (sqlc.narg(enabled)::boolean IS NULL OR enabled = sqlc.narg(enabled)::boolean)
ORDER BY priority ASC, id ASC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: DeleteQuotaPolicy :one
-- 硬删 (quota_policies 无 deleted_at); 若有 quota_windows 引用, FK ON DELETE
-- RESTRICT 触发 23503 -> handler 归一 409 quota_policy_in_use。双定位
-- tenant_id+id; 缺行 -> ErrNoRows -> handler 404。
DELETE FROM quota_policies
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
RETURNING id;
