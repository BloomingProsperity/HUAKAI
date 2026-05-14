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
FROM provider_accounts
WHERE tenant_id = sqlc.arg(tenant_id)
  AND channel_id = sqlc.arg(channel_id)
  AND enabled = true
  AND deleted_at IS NULL
  AND health_state IN ('operational', 'degraded')
ORDER BY priority, last_dispatch_at NULLS FIRST;

-- name: ListEligibleAccountsByPoolGroup :many
-- Phase C.2: pool-group-keyed eligibility lookup for the gateway selector.
-- Joins channels → provider_accounts so a SelectionRequest with PoolGroupID
-- (and no explicit ChannelID) can resolve to the candidate account set.
-- cap_queue_sticky/fallback are returned so the selector can construct
-- WaitPlan fallback when every eligible account is at concurrency cap.
SELECT
    pa.id,
    pa.tenant_id,
    pa.provider_id,
    pa.channel_id,
    pa.cap_concurrency,
    pa.in_flight_count,
    pa.priority,
    pa.last_dispatch_at,
    pa.model_allow_list,
    pa.cap_queue_sticky,
    pa.cap_queue_fallback
FROM provider_accounts pa
INNER JOIN channels c ON c.id = pa.channel_id
WHERE pa.tenant_id = sqlc.arg(tenant_id)
  AND c.pool_group_id = sqlc.arg(pool_group_id)
  AND c.tenant_id = sqlc.arg(tenant_id)
  AND pa.enabled = true
  AND pa.deleted_at IS NULL
  AND pa.health_state IN ('operational', 'degraded')
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
    id,
    tenant_id,
    provider_id,
    expires_at
FROM provider_accounts
WHERE deleted_at IS NULL
  AND enabled
  AND (expires_at IS NULL OR expires_at < sqlc.arg(refresh_before))
ORDER BY COALESCE(expires_at, NOW() + interval '1 year') ASC
LIMIT sqlc.arg(limit_count);

-- name: InsertProviderAccount :one
INSERT INTO provider_accounts (
    tenant_id,
    provider_id,
    channel_id,
    name,
    account_type,
    enabled,
    expires_at,
    credentials,
    cap_concurrency,
    cap_queue_sticky,
    cap_queue_fallback,
    priority,
    model_allow_list,
    capability_flags,
    created_by_actor,
    last_modified_by_actor
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(provider_id)::bigint,
    sqlc.arg(channel_id)::bigint,
    sqlc.arg(name)::text,
    sqlc.arg(account_type)::text,
    COALESCE(sqlc.narg(enabled)::boolean, true),
    sqlc.narg(expires_at)::timestamptz,
    sqlc.arg(credentials)::jsonb,
    COALESCE(sqlc.narg(cap_concurrency)::integer, 4),
    COALESCE(sqlc.narg(cap_queue_sticky)::integer, 2),
    COALESCE(sqlc.narg(cap_queue_fallback)::integer, 8),
    COALESCE(sqlc.narg(priority)::integer, 100),
    COALESCE(sqlc.narg(model_allow_list)::text[], ARRAY[]::text[]),
    COALESCE(sqlc.narg(capability_flags)::text[], ARRAY[]::text[]),
    sqlc.narg(actor_id)::text,
    sqlc.narg(actor_id)::text
)
RETURNING id;

-- name: UpdateProviderAccountEnabled :exec
UPDATE provider_accounts
SET
    enabled = sqlc.arg(enabled)::boolean,
    updated_at = NOW(),
    last_modified_by_actor = sqlc.narg(actor_id)::text
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;

-- name: SoftDeleteProviderAccount :exec
UPDATE provider_accounts
SET
    deleted_at = COALESCE(deleted_at, NOW()),
    updated_at = NOW(),
    enabled = false,
    last_modified_by_actor = sqlc.narg(actor_id)::text
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;
