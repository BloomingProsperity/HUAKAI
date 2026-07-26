-- 内容审核租户配置查询。

-- name: GetModerationConfig :one
SELECT tenant_id, enabled, fail_closed, sample_rate_pct, ban_threshold,
       ban_window_seconds, auto_disable_key_on_ban,
       violation_fee_usd, updated_by, updated_at
FROM moderation_config
WHERE tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: UpsertModerationConfig :one
INSERT INTO moderation_config (
    tenant_id, enabled, fail_closed, sample_rate_pct,
    ban_threshold, ban_window_seconds, auto_disable_key_on_ban,
    violation_fee_usd, updated_by
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(enabled)::boolean,
    sqlc.arg(fail_closed)::boolean,
    sqlc.arg(sample_rate_pct)::integer,
    sqlc.arg(ban_threshold)::integer,
    sqlc.arg(ban_window_seconds)::integer,
    sqlc.arg(auto_disable_key_on_ban)::boolean,
    sqlc.arg(violation_fee_usd)::numeric,
    sqlc.narg(updated_by)::text
)
ON CONFLICT (tenant_id) DO UPDATE
SET enabled = EXCLUDED.enabled,
    fail_closed = EXCLUDED.fail_closed,
    sample_rate_pct = EXCLUDED.sample_rate_pct,
    ban_threshold = EXCLUDED.ban_threshold,
    ban_window_seconds = EXCLUDED.ban_window_seconds,
    auto_disable_key_on_ban = EXCLUDED.auto_disable_key_on_ban,
    violation_fee_usd = EXCLUDED.violation_fee_usd,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING tenant_id, enabled, fail_closed, sample_rate_pct, ban_threshold,
          ban_window_seconds, auto_disable_key_on_ban,
          violation_fee_usd, updated_by, updated_at;
