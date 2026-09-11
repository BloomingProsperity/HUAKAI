-- name: AggregateTenantUsageOverviewTotals :one
-- 租户作用域经营总览 totals。tenant_id 强制，禁止省略成全平台。
-- 列口径与平台总览一致：请求数、实扣费用、输入/输出/提示缓存写读/图像 Token
-- 与费用分项、活跃用户/密钥、成功数。不把网关响应缓存命中折进提示缓存 Token。
SELECT
    count(*)::bigint                                             AS request_count,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text        AS total_cost,
    COALESCE(sum(ur.tokens_input::bigint + ur.tokens_output::bigint), 0)::bigint AS total_tokens,
    COALESCE(sum(ur.tokens_input), 0)::bigint                    AS total_tokens_input,
    COALESCE(sum(ur.tokens_output), 0)::bigint                   AS total_tokens_output,
    COALESCE(sum(ur.cache_creation_tokens), 0)::bigint           AS total_cache_creation_tokens,
    COALESCE(sum(ur.cache_read_tokens), 0)::bigint               AS total_cache_read_tokens,
    COALESCE(sum(ur.image_output_tokens), 0)::bigint             AS total_image_output_tokens,
    COALESCE(sum(ur.input_cost), 0)::numeric(20,8)::text         AS total_input_cost,
    COALESCE(sum(ur.output_cost), 0)::numeric(20,8)::text        AS total_output_cost,
    COALESCE(sum(ur.cache_creation_cost), 0)::numeric(20,8)::text AS total_cache_creation_cost,
    COALESCE(sum(ur.cache_read_cost), 0)::numeric(20,8)::text    AS total_cache_read_cost,
    COALESCE(sum(ur.image_output_cost), 0)::numeric(20,8)::text  AS total_image_output_cost,
    count(DISTINCT ur.user_id)::bigint                           AS active_users,
    count(DISTINCT ur.api_key_id)::bigint                        AS active_api_keys,
    count(*) FILTER (WHERE ur.end_class IN ('stream_end_graceful', 'non_streaming'))::bigint AS success_count
FROM usage_records ur
WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ur.settled_at >= sqlc.arg(settled_since)::timestamptz;

-- name: AggregateTenantUsageOverviewTrendByDay :many
-- 同一租户、同一结算窗口的 UTC 日桶请求数与实扣费用。
SELECT
    (date_trunc('day', ur.settled_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS day,
    count(*)::bigint                                             AS request_count,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text        AS total_cost
FROM usage_records ur
WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ur.settled_at >= sqlc.arg(settled_since)::timestamptz
GROUP BY 1
ORDER BY 1 ASC;

-- name: AggregateTenantUsageHourlyTrend :many
-- 同一租户、同一结算窗口的 UTC 小时桶：输入/输出/提示缓存写读 Token 与实扣费用。
-- 不补零点；不把网关响应缓存命中折进提示缓存 Token。
SELECT
    (date_trunc('hour', ur.settled_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS hour,
    COALESCE(sum(ur.tokens_input), 0)::bigint                    AS tokens_input,
    COALESCE(sum(ur.tokens_output), 0)::bigint                   AS tokens_output,
    COALESCE(sum(ur.cache_creation_tokens), 0)::bigint           AS cache_creation_tokens,
    COALESCE(sum(ur.cache_read_tokens), 0)::bigint               AS cache_read_tokens,
    COALESCE(sum(ur.actual_cost), 0)::numeric(20,8)::text        AS total_cost
FROM usage_records ur
WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ur.settled_at >= sqlc.arg(settled_since)::timestamptz
GROUP BY 1
ORDER BY 1 ASC;
