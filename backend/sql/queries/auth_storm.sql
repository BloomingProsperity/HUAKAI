-- name: GetOrCreateAccountStormBudget :one
WITH inserted AS (
    INSERT INTO oauth_storm_budget (
        scope_type,
        tenant_id,
        provider_account_id,
        cap_concurrent_refreshes,
        cap_refreshes_per_minute
    ) VALUES (
        'account',
        sqlc.arg(tenant_id),
        sqlc.arg(provider_account_id),
        1,
        60
    )
    ON CONFLICT (tenant_id, provider_account_id)
    WHERE scope_type = 'account'
    DO NOTHING
    RETURNING *
)
SELECT *
FROM inserted
UNION ALL
SELECT *
FROM oauth_storm_budget
WHERE scope_type = 'account'
  AND tenant_id = sqlc.arg(tenant_id)
  AND provider_account_id = sqlc.arg(provider_account_id)
LIMIT 1;

-- name: TryAcquireAccountStormSlot :one
WITH acquired AS (
    UPDATE oauth_storm_budget
    SET
        current_in_flight = current_in_flight + 1,
        last_updated_at = NOW()
    WHERE id = sqlc.arg(id)
      AND current_in_flight < cap_concurrent_refreshes
    RETURNING current_in_flight
)
SELECT current_in_flight
FROM acquired
UNION ALL
SELECT 0::integer AS current_in_flight
WHERE NOT EXISTS (SELECT 1 FROM acquired)
LIMIT 1;

-- name: ReleaseAccountStormSlot :exec
UPDATE oauth_storm_budget
SET
    current_in_flight = GREATEST(current_in_flight - 1, 0),
    last_updated_at = NOW()
WHERE id = sqlc.arg(id);

-- name: ReapStaleAccountStormSlots :execrows
-- current_in_flight 是持久计数器: release 失败/进程崩溃会留下永久 +1(cap=1 时该账号
-- 永久无法刷新)。每次 acquire/release 都会刷新 last_updated_at, 正常刷新分钟级完成;
-- 凡 in_flight>0 且该列早于陈旧阈值的行 = 泄漏, 直接归零自愈。
UPDATE oauth_storm_budget
SET current_in_flight = 0,
    last_updated_at = NOW()
WHERE scope_type = 'account'
  AND current_in_flight > 0
  AND last_updated_at <= sqlc.arg(stale_before)::timestamptz;
