-- name: SetProviderAccountModelRateLimit :exec
WITH updated AS (
    UPDATE provider_accounts pa
    SET
        model_rate_limits = jsonb_set(
            COALESCE(pa.model_rate_limits, '{}'::jsonb),
            ARRAY[sqlc.arg(model_key)::text],
            jsonb_build_object(
                'rate_limit_reset_at', to_jsonb(sqlc.arg(reset_at)::timestamptz),
                'reason', sqlc.arg(reason)::text
            ),
            true
        ),
        updated_at = NOW(),
        last_modified_by_actor = sqlc.arg(actor_id)::text
    WHERE pa.tenant_id = sqlc.arg(tenant_id)
      AND pa.id = sqlc.arg(provider_account_id)
      AND pa.deleted_at IS NULL
    RETURNING pa.tenant_id, pa.id
)
INSERT INTO rate_limit_audit_events (
    tenant_id,
    provider_account_id,
    event_type,
    rate_limit_reason,
    upstream_status_code,
    upstream_request_id,
    payload,
    actor_id
)
SELECT
    tenant_id,
    id,
    'model_rate_limit_set',
    sqlc.arg(reason)::text,
    sqlc.arg(upstream_status_code)::integer,
    NULLIF(sqlc.arg(upstream_request_id)::text, ''),
    jsonb_build_object(
        'model_key', sqlc.arg(model_key)::text,
        'reset_at', sqlc.arg(reset_at)::timestamptz,
        'source_layer', sqlc.arg(source_layer)::text
    ),
    sqlc.arg(actor_id)::text
FROM updated;

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
  AND pa.health_state <> 'revoked'
  AND (
      pa.health_state = 'healthy'
      OR (
          pa.health_state IN ('throttled', 'cooldown')
          AND pa.health_state_until IS NOT NULL
          AND pa.health_state_until <= NOW()
      )
  )
  AND (pa.expires_at IS NULL OR pa.expires_at < sqlc.arg(refresh_before))
ORDER BY COALESCE(pa.expires_at, NOW() + interval '1 year') ASC
LIMIT sqlc.arg(limit_count);

-- name: CountUnhealthyAccountsByTenant :many
SELECT
    health_state,
    COUNT(*)::bigint AS account_count
FROM provider_accounts
WHERE tenant_id = $1
  AND deleted_at IS NULL
  AND enabled
  AND health_state <> 'healthy'
  AND (health_state_until IS NULL OR health_state_until > NOW())
GROUP BY health_state;
