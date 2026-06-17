// 用量分析 2 个零覆盖只读 GET 的纯逻辑（查询参数构造），零依赖 strip-types 单测。
// 后端真码:
//   - usageanalyticshttp/perf_metrics_handler.go  NewPerfMetricsByBucketHandler / parsePerfMetricsBucketQuery
//   - usageanalyticshttp/provider_account_counts_handler.go  NewProviderAccountCountsHandler / parseProviderAccountCountsQuery
// 仅构造 query 参数；省略 undefined 由 client.ts apiGet 统一处理。区间/桶合法性由后端 400 守门。

// perf-metrics/by-bucket 的桶粒度。后端 parsePerfMetricsBucketQuery 只接受 hour|day。
export type PerfBucket = 'hour' | 'day';

// UsageAnalyticsQueryParams 与 client.ts apiGet 第二参形状一致（undefined 值会被 apiGet 过滤省略）。
export type UsageAnalyticsQueryParams = Record<string, string | number | boolean | undefined>;

// buildByBucketParams: GET /v1/admin/usage/perf-metrics/by-bucket 的查询参数。
// 字段名逐字对照后端 query.Get("window"/"bucket"/"limit")。
export function buildByBucketParams(
  bucket: PerfBucket,
  window: string,
  opts?: { limit?: number },
): UsageAnalyticsQueryParams {
  return { window, bucket, limit: opts?.limit };
}

// buildCountsParams: GET /v1/admin/usage/provider-account-counts 的查询参数。
// 后端 parseProviderAccountCountsQuery: from/to(RFC3339)必填、tenant_id 可选正整数。
// from/to 顺序不可互换(后端要求 to>from)，此处原样映射到同名键。
export function buildCountsParams(
  from: string,
  to: string,
  opts?: { tenant_id?: number },
): UsageAnalyticsQueryParams {
  return { from, to, tenant_id: opts?.tenant_id };
}
