-- name: GetAdminProviderAccountHealth :one
SELECT
    pa.id,
    pa.tenant_id,
    COALESCE(p.code, '')::text AS provider_code,
    pa.account_type,
    COALESCE(current_ac.vendor, '')::text AS credential_vendor,
    COALESCE(current_ac.auth_mode, '')::text AS credential_auth_mode,
    current_ac.project_ref AS credential_project_ref,
    COALESCE(current_ac.serving_credential_candidates, 0)::integer AS serving_credential_candidates,
    pa.health_state,
    pa.health_state_until,
    pa.enabled,
    pa.disable_cooling,
    pa.credential_state,
    pa.model_rate_limits,
    pa.rate_limit_reset_at,
    pa.overload_until,
    pa.temp_unschedulable_until,
    EXISTS (
        SELECT 1
        FROM channels c
        WHERE c.id = pa.channel_id
          AND c.tenant_id = pa.tenant_id
          AND c.enabled = true
          AND c.deleted_at IS NULL
    ) AS channel_enabled,
    EXISTS (
        SELECT 1
        FROM providers p
        WHERE p.id = pa.provider_id
          AND p.tenant_id = pa.tenant_id
          AND p.deleted_at IS NULL
    ) AS provider_available,
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
    COALESCE(refresh_ac.last_refresh_at, pa.last_refresh_at) AS last_refresh_at,
    COALESCE(refresh_ac.last_refresh_outcome, pa.last_refresh_outcome) AS last_refresh_outcome,
    refresh_ac.failure_class,
    COALESCE(refresh_ac.failure_count, 0)::integer AS failure_count,
    pa.updated_at
FROM provider_accounts pa
LEFT JOIN providers p
  ON p.id = pa.provider_id
 AND p.tenant_id = pa.tenant_id
LEFT JOIN LATERAL (
    SELECT
        vendor,
        auth_mode,
        project_ref,
        count(*) OVER ()::integer AS serving_credential_candidates
    FROM account_credentials ac
    WHERE ac.tenant_id = pa.tenant_id
      AND ac.provider_account_id = pa.id
      AND ac.deleted_at IS NULL
      AND pa.enabled
      AND (
          ac.state = 'active'
          OR (
              ac.state = 'refreshing_with_grace'
              AND (ac.grace_until IS NULL OR ac.grace_until > now())
          )
      )
    ORDER BY CASE ac.state WHEN 'active' THEN 0 ELSE 1 END, ac.updated_at DESC, ac.id DESC
    LIMIT 1
) current_ac ON true
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
) refresh_ac ON true
WHERE pa.tenant_id = sqlc.arg(tenant_id)::bigint
  AND pa.id = sqlc.arg(id)::bigint
  AND pa.deleted_at IS NULL;

-- name: TouchProviderAccountProbe :exec
-- 由异步请求完成事件调用,沿用旧 last_probe_at 存储列记录被动请求观测时间。
-- 该值不是主动上游探测结果;管理 API 使用 last_request_observed_at 暴露真实语义。
UPDATE provider_accounts
SET last_probe_at = sqlc.arg(probed_at)::timestamptz
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
  AND (last_probe_at IS NULL OR last_probe_at < sqlc.arg(probed_at)::timestamptz);

-- name: SummarizeProviderAccountHealth :many
-- 账号池健康聚合(B9 运维巡检):按 (health_state, enabled) 计数,跨整个租户池(非分页)。
-- 只读、不含钱字段;供管理端一眼看清问题账号分布。软删账号排除。
SELECT health_state, enabled, count(*)::bigint AS n
FROM provider_accounts
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
GROUP BY health_state, enabled
ORDER BY health_state, enabled;
