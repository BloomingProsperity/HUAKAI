-- name: ListEligibleAccounts :many
WITH normalized_health AS (
    UPDATE provider_accounts pa
    SET
        health_state = 'healthy',
        health_state_until = NULL,
        updated_at = NOW()
    WHERE pa.tenant_id = sqlc.arg(tenant_id)
      AND pa.channel_id = sqlc.arg(channel_id)
      AND pa.health_state IN ('throttled', 'revoked', 'cooldown')
      AND pa.health_state_until IS NOT NULL
      AND pa.health_state_until <= NOW()
    RETURNING pa.id
)
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
      OR pa.id IN (SELECT id FROM normalized_health)
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
),
normalized_health AS (
    UPDATE provider_accounts pa
    SET
        health_state = 'healthy',
        health_state_until = NULL,
        updated_at = NOW()
    FROM target_channels tc
    WHERE pa.tenant_id = sqlc.arg(tenant_id)
      AND pa.channel_id = tc.id
      AND pa.health_state IN ('throttled', 'revoked', 'cooldown')
      AND pa.health_state_until IS NOT NULL
      AND pa.health_state_until <= NOW()
    RETURNING pa.id
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
    pa.last_dispatch_at,
    pa.health_state,
    pa.health_state_until,
    pa.model_allow_list,
    pa.capability_flags,
    pa.cap_queue_sticky,
    pa.cap_queue_fallback
FROM provider_accounts pa
INNER JOIN channels c
    ON c.id = pa.channel_id
   AND c.enabled = true
   AND c.deleted_at IS NULL
INNER JOIN providers p
    ON p.id = pa.provider_id
   AND p.tenant_id = pa.tenant_id
   AND p.deleted_at IS NULL
WHERE pa.tenant_id = sqlc.arg(tenant_id)
  AND c.pool_group_id = sqlc.arg(pool_group_id)
  AND c.tenant_id = sqlc.arg(tenant_id)
  AND pa.enabled = true
  AND pa.deleted_at IS NULL
  AND (
      pa.health_state = 'healthy'
      OR pa.id IN (SELECT id FROM normalized_health)
  )
  AND (cardinality(pa.model_allow_list) = 0
       OR pa.model_allow_list @> ARRAY[sqlc.arg(requested_model)::text])
  AND (sqlc.arg(requested_protocol_family)::text = ''
       OR p.upstream_protocol = sqlc.arg(requested_protocol_family)::text)
  AND pa.capability_flags @> sqlc.arg(required_capabilities)::text[]
  -- codex review v3 P2#3 fix: production selector 不接 AuthCredentialGate
  -- (无 TokenProvider 注入), 改 SQL 层直接过滤 credential_state.
  -- 跟 binding.AuthCredentialGate spec 一致: 只放 {valid, refreshing_with_grace}.
  -- 'refreshing' (无 grace) 当短暂状态走线上后被 cooldown 接住; 'refresh_failed'
  -- + 'revoked' 直接跳过, 防 selector 选到已死账号 → 401 后再 cooldown 浪费一轮。
  AND pa.credential_state IN ('valid', 'refreshing_with_grace')
ORDER BY pa.priority, pa.last_dispatch_at NULLS FIRST;

-- name: GetAccountForRevalidation :one
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
FROM provider_accounts
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
FOR UPDATE;

-- name: IncrementInFlightCount :execrows
UPDATE provider_accounts
SET
    in_flight_count = in_flight_count + 1,
    last_dispatch_at = NOW(),
    updated_at = NOW()
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
  AND in_flight_count < cap_concurrency;

-- name: DecrementInFlightCount :exec
UPDATE provider_accounts
SET
    in_flight_count = GREATEST(in_flight_count - 1, 0),
    updated_at = NOW()
WHERE id = sqlc.arg(id);

-- name: GetModelRoutingForGroup :many
SELECT
    model,
    provider_account_ids
FROM model_routing_overrides
WHERE tenant_id = sqlc.arg(tenant_id)
  AND pool_group_id = sqlc.arg(pool_group_id)
  AND model = sqlc.arg(model)
  AND enabled = true
  AND deleted_at IS NULL;

-- name: ListAccountsForRefresh :many
SELECT
    pa.id,
    pa.tenant_id,
    pa.provider_id,
    p.code AS vendor_name,
    pa.expires_at
FROM provider_accounts pa
JOIN providers p
  ON p.id = pa.provider_id
 AND p.tenant_id = pa.tenant_id
 AND p.deleted_at IS NULL
WHERE pa.deleted_at IS NULL
  AND pa.enabled
  AND (pa.expires_at IS NULL OR pa.expires_at < sqlc.arg(refresh_before))
ORDER BY COALESCE(pa.expires_at, NOW() + interval '1 year') ASC
LIMIT sqlc.arg(limit_count);
