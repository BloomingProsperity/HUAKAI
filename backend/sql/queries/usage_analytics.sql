-- Usage analytics: aggregation queries over settled usage_records.
-- SELECT-only. Self-serve queries carry a non-nullable tenant_id
-- predicate (cross-tenant prevention). Admin leaderboard queries are
-- platform-admin-only and intentionally aggregate across tenants for operator
-- cost visibility. No query selects credential columns.
-- usage_records.settled_at is NOT NULL DEFAULT now() and indexed by
-- idx_usage_records_tenant_settled (tenant_id, settled_at DESC), so these
-- aggregations need no new index in the pre-launch first cut.

-- name: AggregateMyUsageByDay :many
-- Self-serve daily usage time-series for ONE API key: cost + token totals
-- bucketed by UTC calendar day and requested_model. Always scoped by
-- (tenant_id, api_key_id) so cross-key reads are structurally impossible —
-- the handler passes ident.APIKeyID, never a client-supplied value.
SELECT
    (date_trunc('day', ur.settled_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS day,
    ur.requested_model,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text  AS total_cost,
    COALESCE(sum(ur.tokens_input), 0)::bigint              AS total_tokens_input,
    COALESCE(sum(ur.tokens_output), 0)::bigint             AS total_tokens_output,
    COALESCE(sum(ur.cache_read_tokens), 0)::bigint         AS total_cache_read_tokens,
    COALESCE(sum(ur.cache_creation_tokens), 0)::bigint     AS total_cache_creation_tokens,
    count(*)::bigint                                       AS request_count
FROM usage_records ur
WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ur.api_key_id = sqlc.arg(api_key_id)::bigint
  AND ur.settled_at >= sqlc.arg(from_ts)::timestamptz
  AND ur.settled_at <  sqlc.arg(to_ts)::timestamptz
GROUP BY 1, 2
ORDER BY 1 DESC, 2 ASC;

-- name: AggregateMyUsageByWeek :many
-- Self-serve weekly usage time-series for ONE API key: cost + token totals
-- bucketed by UTC week start and requested_model. Always scoped by
-- (tenant_id, api_key_id) so cross-key reads are structurally impossible —
-- the handler passes ident.APIKeyID, never a client-supplied value.
SELECT
    (date_trunc('week', ur.settled_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS day,
    ur.requested_model,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text  AS total_cost,
    COALESCE(sum(ur.tokens_input), 0)::bigint              AS total_tokens_input,
    COALESCE(sum(ur.tokens_output), 0)::bigint             AS total_tokens_output,
    COALESCE(sum(ur.cache_read_tokens), 0)::bigint         AS total_cache_read_tokens,
    COALESCE(sum(ur.cache_creation_tokens), 0)::bigint     AS total_cache_creation_tokens,
    count(*)::bigint                                       AS request_count
FROM usage_records ur
WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ur.api_key_id = sqlc.arg(api_key_id)::bigint
  AND ur.settled_at >= sqlc.arg(from_ts)::timestamptz
  AND ur.settled_at <  sqlc.arg(to_ts)::timestamptz
GROUP BY 1, 2
ORDER BY 1 DESC, 2 ASC;

-- name: AggregateMyUsageByMonth :many
-- Self-serve monthly usage time-series for ONE API key: cost + token totals
-- bucketed by UTC month start and requested_model. Always scoped by
-- (tenant_id, api_key_id) so cross-key reads are structurally impossible —
-- the handler passes ident.APIKeyID, never a client-supplied value.
SELECT
    (date_trunc('month', ur.settled_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS day,
    ur.requested_model,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text  AS total_cost,
    COALESCE(sum(ur.tokens_input), 0)::bigint              AS total_tokens_input,
    COALESCE(sum(ur.tokens_output), 0)::bigint             AS total_tokens_output,
    COALESCE(sum(ur.cache_read_tokens), 0)::bigint         AS total_cache_read_tokens,
    COALESCE(sum(ur.cache_creation_tokens), 0)::bigint     AS total_cache_creation_tokens,
    count(*)::bigint                                       AS request_count
FROM usage_records ur
WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ur.api_key_id = sqlc.arg(api_key_id)::bigint
  AND ur.settled_at >= sqlc.arg(from_ts)::timestamptz
  AND ur.settled_at <  sqlc.arg(to_ts)::timestamptz
GROUP BY 1, 2
ORDER BY 1 DESC, 2 ASC;

-- name: AggregateMyUsageTotals :one
-- Self-serve single-row usage totals for one caller-owned API key. The handler
-- first verifies (tenant_id, user_id, api_key_id) ownership through userkey
-- service; this query still carries tenant_id + api_key_id predicates so a
-- handler bug cannot widen the read to another tenant or another key.
SELECT
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text  AS total_cost,
    COALESCE(sum(ur.tokens_input), 0)::bigint              AS total_tokens_input,
    COALESCE(sum(ur.tokens_output), 0)::bigint             AS total_tokens_output,
    COALESCE(sum(ur.cache_read_tokens), 0)::bigint         AS total_cache_read_tokens,
    COALESCE(sum(ur.cache_creation_tokens), 0)::bigint     AS total_cache_creation_tokens,
    count(*)::bigint                                       AS request_count
FROM usage_records ur
WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ur.api_key_id = sqlc.arg(api_key_id)::bigint
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR ur.settled_at >= sqlc.narg(from_ts)::timestamptz)
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR ur.settled_at < sqlc.narg(to_ts)::timestamptz);

-- name: AggregateUsageLeaderboardByUser :many
-- Platform-admin cost leaderboard by user_id. This is the operator surface:
-- actual_cost is intentionally used to show real upstream spend.
SELECT
    ur.user_id::text                                             AS key,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text        AS total_cost,
    COALESCE(sum(ur.tokens_input::bigint + ur.tokens_output::bigint), 0)::bigint AS total_tokens,
    count(*)::bigint                                             AS request_count
FROM usage_records ur
WHERE ur.settled_at >= sqlc.arg(settled_since)::timestamptz
GROUP BY ur.user_id
ORDER BY sum(ur.actual_cost) DESC, key ASC
LIMIT sqlc.arg(row_limit)::int;

-- name: AggregateUsageLeaderboardByModel :many
-- Platform-admin cost leaderboard by requested_model.
SELECT
    ur.requested_model                                           AS key,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text        AS total_cost,
    COALESCE(sum(ur.tokens_input::bigint + ur.tokens_output::bigint), 0)::bigint AS total_tokens,
    count(*)::bigint                                             AS request_count
FROM usage_records ur
WHERE ur.settled_at >= sqlc.arg(settled_since)::timestamptz
GROUP BY ur.requested_model
ORDER BY sum(ur.actual_cost) DESC, key ASC
LIMIT sqlc.arg(row_limit)::int;

-- name: AggregateUsageLeaderboardByProviderAccount :many
-- Platform-admin cost leaderboard by provider_account_id. Provider-less
-- usage, such as cache-only settlement, is grouped under "unassigned".
SELECT
    (CASE
        WHEN ur.provider_account_id IS NULL THEN 'unassigned'
        ELSE ur.provider_account_id::text
     END)::text                                                  AS key,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text        AS total_cost,
    COALESCE(sum(ur.tokens_input::bigint + ur.tokens_output::bigint), 0)::bigint AS total_tokens,
    count(*)::bigint                                             AS request_count
FROM usage_records ur
WHERE ur.settled_at >= sqlc.arg(settled_since)::timestamptz
GROUP BY ur.provider_account_id
ORDER BY sum(ur.actual_cost) DESC, key ASC
LIMIT sqlc.arg(row_limit)::int;

-- name: AggregateUsageLeaderboardByApiKey :many
-- Platform-admin cost leaderboard by api_key_id. Passing tenant_id=0 keeps
-- the existing global admin leaderboard behavior; a positive tenant_id narrows
-- the read-only rollup to that tenant for tenant-focused ops drilldown.
SELECT
    ur.api_key_id::text                                          AS key,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text        AS total_cost,
    COALESCE(sum(ur.tokens_input::bigint + ur.tokens_output::bigint), 0)::bigint AS total_tokens,
    count(*)::bigint                                             AS request_count
FROM usage_records ur
WHERE ur.settled_at >= sqlc.arg(settled_since)::timestamptz
  AND (sqlc.arg(tenant_id)::bigint = 0 OR ur.tenant_id = sqlc.arg(tenant_id)::bigint)
GROUP BY ur.api_key_id
ORDER BY sum(ur.actual_cost) DESC, key ASC
LIMIT sqlc.arg(row_limit)::int;

-- name: AggregateUsagePerformanceByModel :many
-- Platform-admin performance panel by requested_model. Read-only operator
-- surface: latency, throughput, and error rate inputs only; no cost fields.
SELECT
    ur.requested_model                                           AS key,
    COALESCE(
        avg(EXTRACT(EPOCH FROM (ur.first_byte_at - ur.requested_at)) * 1000)
            FILTER (WHERE ur.first_byte_at IS NOT NULL),
        0
    )::numeric(20,4)::text                                       AS avg_ttft_ms,
    COALESCE(
        avg(ur.tokens_output::numeric / NULLIF(EXTRACT(EPOCH FROM (ur.last_event_at - ur.first_byte_at)), 0))
            FILTER (WHERE ur.last_event_at IS NOT NULL AND ur.first_byte_at IS NOT NULL AND ur.tokens_output > 0),
        0
    )::numeric(20,4)::text                                       AS avg_tps,
    count(*)::bigint                                             AS request_count,
    count(*) FILTER (WHERE ur.end_class NOT IN ('stream_end_graceful', 'non_streaming'))::bigint AS error_count
FROM usage_records ur
WHERE ur.settled_at >= sqlc.arg(settled_since)::timestamptz
GROUP BY ur.requested_model
ORDER BY count(*) DESC, key ASC
LIMIT sqlc.arg(row_limit)::int;

-- name: AggregateUsagePerformanceByProviderAccount :many
-- Platform-admin performance panel by provider_account_id. Provider-less
-- usage, such as cache-only settlement, is grouped under "unassigned".
SELECT
    (CASE
        WHEN ur.provider_account_id IS NULL THEN 'unassigned'
        ELSE ur.provider_account_id::text
     END)::text                                                  AS key,
    COALESCE(
        avg(EXTRACT(EPOCH FROM (ur.first_byte_at - ur.requested_at)) * 1000)
            FILTER (WHERE ur.first_byte_at IS NOT NULL),
        0
    )::numeric(20,4)::text                                       AS avg_ttft_ms,
    COALESCE(
        avg(ur.tokens_output::numeric / NULLIF(EXTRACT(EPOCH FROM (ur.last_event_at - ur.first_byte_at)), 0))
            FILTER (WHERE ur.last_event_at IS NOT NULL AND ur.first_byte_at IS NOT NULL AND ur.tokens_output > 0),
        0
    )::numeric(20,4)::text                                       AS avg_tps,
    count(*)::bigint                                             AS request_count,
    count(*) FILTER (WHERE ur.end_class NOT IN ('stream_end_graceful', 'non_streaming'))::bigint AS error_count
FROM usage_records ur
WHERE ur.settled_at >= sqlc.arg(settled_since)::timestamptz
GROUP BY ur.provider_account_id
ORDER BY count(*) DESC, key ASC
LIMIT sqlc.arg(row_limit)::int;

-- name: AggregateUsageOverviewTotals :one
-- Platform-admin overview totals across the recent settled usage window.
-- Operator surface: actual_cost is intentionally exposed as decimal text.
SELECT
    count(*)::bigint                                             AS request_count,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text        AS total_cost,
    COALESCE(sum(ur.tokens_input::bigint + ur.tokens_output::bigint), 0)::bigint AS total_tokens,
    count(DISTINCT ur.user_id)::bigint                           AS active_users,
    count(DISTINCT ur.api_key_id)::bigint                        AS active_api_keys,
    count(*) FILTER (WHERE ur.end_class IN ('stream_end_graceful', 'non_streaming'))::bigint AS success_count
FROM usage_records ur
WHERE ur.settled_at >= sqlc.arg(settled_since)::timestamptz;

-- name: AggregateUsageOverviewTrendByDay :many
-- Platform-admin overview daily trend across the recent settled usage window.
SELECT
    (date_trunc('day', ur.settled_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS day,
    count(*)::bigint                                             AS request_count,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text        AS total_cost
FROM usage_records ur
WHERE ur.settled_at >= sqlc.arg(settled_since)::timestamptz
GROUP BY 1
ORDER BY 1 ASC;
