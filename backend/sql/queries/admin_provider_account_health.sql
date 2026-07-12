-- name: GetAdminProviderAccountHealth :one
SELECT
    pa.id,
    pa.tenant_id,
    pa.health_state,
    pa.health_state_until,
    pa.enabled,
    pa.last_probe_latency_ms,
    pa.last_probe_at,
    pa.model_sync_last_check_at,
    pa.session_window_5h_start,
    pa.session_window_5h_end,
    pa.session_window_5h_status,
    pa.session_window_5h_utilization,
    pa.session_window_7d_start,
    pa.session_window_7d_end,
    pa.session_window_7d_status,
    pa.session_window_7d_utilization,
    COALESCE(ac.last_refresh_at, pa.last_refresh_at) AS last_refresh_at,
    COALESCE(ac.last_refresh_outcome, pa.last_refresh_outcome) AS last_refresh_outcome,
    ac.failure_class,
    COALESCE(ac.failure_count, 0)::integer AS failure_count,
    pa.updated_at
FROM provider_accounts pa
LEFT JOIN LATERAL (
    SELECT
        last_refresh_at,
        last_refresh_outcome,
        failure_class,
        failure_count
    FROM account_credentials ac
    WHERE ac.tenant_id = pa.tenant_id
      AND ac.provider_account_id = pa.id
      AND ac.deleted_at IS NULL
      AND ac.state NOT IN ('revoked')
    ORDER BY ac.last_refresh_at DESC NULLS LAST, ac.updated_at DESC, ac.credential_version DESC, ac.id DESC
    LIMIT 1
) ac ON true
WHERE pa.tenant_id = sqlc.arg(tenant_id)::bigint
  AND pa.id = sqlc.arg(id)::bigint
  AND pa.deleted_at IS NULL;

-- name: TouchProviderAccountProbe :exec
-- 由异步 eventbus account_health_probe handler 调用:每次请求完成在对应池账号上
-- 盖一个"最近探测时间"戳,点亮运维健康面板的 last_probe_at 列(迁移 0110 早已加列,
-- 但此前全仓零写入,该列恒 NULL)。纯可观测写,单行 PK 定位,不碰钱/auth/health_state。
-- last_probe_latency_ms 暂留 follow-up(请求延迟分散在多个发射点,见计划工件)。
UPDATE provider_accounts
SET last_probe_at = sqlc.arg(probed_at)::timestamptz
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;
