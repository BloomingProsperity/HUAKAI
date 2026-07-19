-- HUAKAI 配额窗口写入。所有读写都由 tenant_id 与窗口主键共同定位。

-- name: UpsertQuotaWindow :one
-- 为 policy/window_start 建立或复用窗口行。
WITH upserted AS (
    INSERT INTO quota_windows (
        tenant_id, policy_id, window_start, window_end
    )
    SELECT
        qp.tenant_id,
        qp.id,
        sqlc.arg(window_start)::timestamptz,
        sqlc.arg(window_end)::timestamptz
    FROM quota_policies qp
    WHERE qp.tenant_id = sqlc.arg(tenant_id)::bigint
      AND qp.id = sqlc.arg(policy_id)::bigint
    ON CONFLICT ON CONSTRAINT uq_quota_windows_policy_start
    DO UPDATE SET
        window_end = EXCLUDED.window_end,
        updated_at = NOW()
    WHERE quota_windows.tenant_id = sqlc.arg(tenant_id)::bigint
    RETURNING
        tenant_id,
        id,
        policy_id,
        window_start,
        window_end,
        reserved_value,
        settled_value,
        overage_value,
        request_count,
        version
)
SELECT
    upserted.tenant_id,
    upserted.id,
    upserted.policy_id,
    upserted.window_start,
    upserted.window_end,
    upserted.reserved_value,
    upserted.settled_value,
    upserted.overage_value,
    upserted.request_count,
    upserted.version,
    qp.window_kind,
    qp.window_seconds
FROM upserted
JOIN quota_policies qp
  ON qp.tenant_id = upserted.tenant_id
 AND qp.id = upserted.policy_id;

-- name: GetQuotaWindowForUpdate :one
SELECT
    tenant_id,
    id,
    policy_id,
    window_start,
    window_end,
    reserved_value,
    settled_value,
    overage_value,
    request_count,
    version
FROM quota_windows
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(window_id)::bigint
FOR UPDATE;

-- name: IncrementQuotaWindowReserved :one
-- reserved + settled + delta 不得超过调用方传入的策略上限。
UPDATE quota_windows
SET reserved_value = reserved_value + sqlc.arg(reserve_delta)::numeric(20,8),
    request_count = request_count + sqlc.arg(request_count_delta)::bigint,
    version = version + 1,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(window_id)::bigint
  AND reserved_value + settled_value + sqlc.arg(reserve_delta)::numeric(20,8)
      <= sqlc.arg(limit_value)::numeric(20,8)
RETURNING
    tenant_id,
    id,
    reserved_value,
    settled_value,
    overage_value,
    request_count,
    version;

-- name: IncrementQuotaWindowRequestCount :one
-- request_count 只作为镜像，准入使用 reserved_value + settled_value。
UPDATE quota_windows
SET request_count = request_count + sqlc.arg(request_count_delta)::bigint,
    version = version + 1,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(window_id)::bigint
  AND request_count + sqlc.arg(request_count_delta)::bigint
      <= sqlc.arg(limit_value)::numeric(20,8)
RETURNING
    tenant_id,
    id,
    reserved_value,
    settled_value,
    overage_value,
    request_count,
    version;

-- name: ApplyQuotaWindowSettlement :one
-- 结算阶段把 reserved 转为 settled，实际超出部分进入 overage。
UPDATE quota_windows
SET reserved_value = GREATEST(
        0::numeric(20,8),
        reserved_value - sqlc.arg(reserved_release_value)::numeric(20,8)
    ),
    settled_value = settled_value + sqlc.arg(settled_add_value)::numeric(20,8),
    overage_value = overage_value + sqlc.arg(overage_add_value)::numeric(20,8),
    version = version + 1,
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(window_id)::bigint
RETURNING
    tenant_id,
    id,
    reserved_value,
    settled_value,
    overage_value,
    request_count,
    version;
