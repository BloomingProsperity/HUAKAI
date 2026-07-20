-- name: ListEligibleAccounts :many
SELECT
    id,
    tenant_id,
    provider_id,
    channel_id,
    name,
    account_type,
    enabled,
    expires_at,
    health_state,
    health_state_until,
    credential_state,
    credentials,
    cap_concurrency,
    in_flight_count,
    cap_queue_sticky,
    cap_queue_fallback,
    queue_depth,
    priority,
    last_dispatch_at,
    model_allow_list,
    capability_flags,
    cap_quota_total,
    quota_used_total,
    cap_quota_daily,
    quota_used_daily,
    quota_window_daily_start,
    cap_quota_weekly,
    quota_used_weekly,
    quota_window_weekly_start,
    quota_status,
    created_at,
    updated_at,
    deleted_at,
    created_by_actor,
    last_modified_by_actor,
    rate_limited_at,
    rate_limit_reset_at,
    rate_limit_reason,
    overload_until,
    temp_unschedulable_until,
    temp_unschedulable_reason,
    temp_unschedulable_rule_index,
    session_window_5h_start,
    session_window_5h_end,
    session_window_5h_status,
    openai_403_counter,
    openai_403_window_start,
    custom_error_codes_enabled,
    custom_error_codes,
    pool_mode,
    temp_unschedulable_enabled,
    temp_unschedulable_rules,
    model_rate_limits,
    refresh_attempt_count,
    refresh_attempt_window_start,
    token_version,
    refresh_token_fingerprint,
    last_refresh_at,
    last_refresh_outcome,
    oauth_endpoint_health
FROM provider_accounts pa
WHERE pa.tenant_id = sqlc.arg(tenant_id)
  AND pa.channel_id = sqlc.arg(channel_id)
  AND pa.enabled = true
  AND pa.deleted_at IS NULL
  AND (
      pa.health_state = 'healthy'
      OR (
          pa.health_state IN ('throttled', 'revoked', 'cooldown')
          AND pa.health_state_until IS NOT NULL
          AND pa.health_state_until <= NOW()
      )
  )
  -- 凭据真相门:只放至少有一条可服务凭据的账号。真相在 account_credentials.state
  -- (credentialstore 写生命周期),不是冻死的 provider_accounts.credential_state
  -- (无生命周期写点、恒 'valid' → 虚设过滤且放空壳账号进池)。谓词逐字匹配物化
  -- resolveActiveQuery 的可服务判定(active / grace 未过期),使"选到却物化不出"归零。
  AND EXISTS (
      SELECT 1 FROM account_credentials ac
      WHERE ac.provider_account_id = pa.id
        AND ac.tenant_id = pa.tenant_id
        AND ac.deleted_at IS NULL
        AND (
            ac.state = 'active'
            OR (ac.state = 'refreshing_with_grace' AND (ac.grace_until IS NULL OR ac.grace_until > NOW()))
        )
  )
ORDER BY priority, last_dispatch_at NULLS FIRST;

-- name: ListEligibleAccountsByPoolGroup :many
WITH target_channels AS (
    SELECT c.id
    FROM channels c
    WHERE c.pool_group_id = sqlc.arg(pool_group_id)
      AND c.tenant_id = sqlc.arg(tenant_id)
      AND c.enabled = true
      AND c.deleted_at IS NULL
)
-- Phase C.2: pool-group-keyed eligibility lookup for the gateway selector.
-- Joins channels → provider_accounts so a SelectionRequest with PoolGroupID
-- (and no explicit ChannelID) can resolve to the candidate account set.
-- cap_queue_sticky/fallback are returned so the selector can construct
-- WaitPlan fallback when every eligible account is at concurrency cap.
--
-- 2026-05-19 codex review P1 fix: 之前不过滤 model_allow_list /
-- capability_flags, production gate AllowAll 全过, request 能 reserve
-- 到明确不被该 account 允许的 model / 缺能力。两个 filter 直接在 SQL
-- 层做 (Postgres array @> 子集 + cardinality empty bypass):
--   - model_allow_list 空 数组 → 无限制
--   - model_allow_list 非空 → 必须包含 requested_model
--   - capability_flags 必须包含 required_capabilities 全集 (空 req → 自动 true)
--   - requested_protocol_family 为空 → legacy bypass
--   - requested_protocol_family 非空 → 必须匹配 providers.upstream_protocol
SELECT
    pa.id,
    pa.tenant_id,
    pa.provider_id,
    p.upstream_protocol,
    pa.channel_id,
    pa.cap_concurrency,
    pa.in_flight_count,
    pa.priority,
    pa.static_weight,
    pa.upstream_cost_ratio,
    pa.last_dispatch_at,
    pa.health_state,
    pa.health_state_until,
    pa.model_rate_limits,
    pa.model_allow_list,
    pa.capability_flags,
    pa.cap_queue_sticky,
    pa.cap_queue_fallback,
    pa.window_cost_limit_cents,
    pa.max_sessions,
    pa.disable_cooling,
    pa.rpm_limit,
    pa.tpm_limit,
    rs.success_ewma,
    rs.error_ewma,
    rs.response_latency_ms_ewma,
    rs.sample_count AS routing_signal_sample_count,
    rs.observed_at AS routing_signal_observed_at,
    quota.state AS upstream_quota_state,
    CASE WHEN quota.remaining_percent IS NULL THEN false ELSE true END AS upstream_quota_remaining_known,
    COALESCE(quota.remaining_percent, 0::double precision) AS upstream_quota_remaining_percent,
    quota.resets_at AS upstream_quota_resets_at,
    quota.observed_at AS upstream_quota_observed_at
FROM provider_accounts pa
INNER JOIN channels c
    ON c.id = pa.channel_id
   AND c.enabled = true
   AND c.deleted_at IS NULL
INNER JOIN providers p
    ON p.id = pa.provider_id
   AND p.tenant_id = pa.tenant_id
   AND p.deleted_at IS NULL
LEFT JOIN provider_account_routing_signals rs
    ON rs.tenant_id = pa.tenant_id
   AND rs.provider_account_id = pa.id
LEFT JOIN LATERAL (
    SELECT
        CASE
            WHEN bool_or(q.state = 'exhausted') THEN 'exhausted'
            WHEN bool_or(q.state = 'available') THEN 'available'
            WHEN bool_or(q.state = 'error') THEN 'error'
            ELSE 'unknown'
        END::text AS state,
        (min(q.remaining_percent) FILTER (WHERE q.state IN ('available', 'exhausted')))::double precision AS remaining_percent,
        (min(q.resets_at) FILTER (WHERE q.state IN ('available', 'exhausted')))::timestamptz AS resets_at,
        max(q.observed_at)::timestamptz AS observed_at
    FROM provider_account_quota_facts q
    WHERE q.tenant_id = pa.tenant_id
      AND q.provider_account_id = pa.id
      AND q.metric_key <> 'probe_status'
      AND (q.model_key = '' OR q.model_key = sqlc.arg(requested_model)::text)
      AND q.observed_at > NOW() - INTERVAL '2 hours'
      AND (q.valid_until IS NULL OR q.valid_until > NOW())
) quota ON true
WHERE pa.tenant_id = sqlc.arg(tenant_id)
  AND c.pool_group_id = sqlc.arg(pool_group_id)
  AND c.tenant_id = sqlc.arg(tenant_id)
  AND pa.enabled = true
  AND pa.deleted_at IS NULL
  -- 未设过期时间(NULL)的账号不受影响;已过期账号排除出选号候选。
  AND (pa.expires_at IS NULL OR pa.expires_at > NOW())
  AND (
      pa.health_state = 'healthy'
      OR (
          pa.health_state IN ('throttled', 'revoked', 'cooldown')
          AND pa.health_state_until IS NOT NULL
          AND pa.health_state_until <= NOW()
      )
  )
  AND (cardinality(pa.model_allow_list) = 0
       OR pa.model_allow_list @> ARRAY[sqlc.arg(requested_model)::text])
  AND (sqlc.arg(requested_protocol_family)::text = ''
       OR p.upstream_protocol = sqlc.arg(requested_protocol_family)::text)
  AND pa.capability_flags @> sqlc.arg(required_capabilities)::text[]
  -- 凭据真相门:只放至少有一条可服务凭据的账号。此前过滤 pa.credential_state
  -- (冻死列、无生命周期写点、恒 'valid') 是虚设过滤且放空壳账号进池;改读真相
  -- account_credentials.state(credentialstore 写生命周期)。谓词逐字匹配物化
  -- resolveActiveQuery 的可服务判定(active / grace 未过期),防 selector 选到无
  -- 可用凭据账号 → 物化落空浪费一轮。EXISTS 保一账号一行(多凭据不放大行数)。
  AND EXISTS (
      SELECT 1 FROM account_credentials ac
      WHERE ac.provider_account_id = pa.id
        AND ac.tenant_id = pa.tenant_id
        AND ac.deleted_at IS NULL
        AND (
            ac.state = 'active'
            OR (ac.state = 'refreshing_with_grace' AND (ac.grace_until IS NULL OR ac.grace_until > NOW()))
        )
  )
ORDER BY pa.priority, pa.last_dispatch_at NULLS FIRST;
