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
