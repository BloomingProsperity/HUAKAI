-- Usage analytics: aggregation queries over settled usage_records.
-- SELECT-only (CMB-7). Self-serve queries carry a non-nullable tenant_id
-- predicate (CMB-5 cross-tenant prevention). Admin leaderboard queries are
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
    date_trunc('day', ur.settled_at AT TIME ZONE 'UTC')::timestamptz AS day,
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
