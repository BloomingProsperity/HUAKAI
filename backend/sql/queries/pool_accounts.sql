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
