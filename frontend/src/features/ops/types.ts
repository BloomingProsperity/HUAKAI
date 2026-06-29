/*
 * Ops 运维大屏(运维台)前端类型 —— 镜像 usageanalyticshttp 的 JSON。
 * 端点(全 admin token 鉴权,只读):
 *   GET /v1/admin/usage/overview            总览(请求/成本/token/活跃/成功率 + 日趋势)
 *   GET /v1/admin/usage/perf-metrics/summary 性能(p50/p95/p99 + TTFT/TPS + 错误率)
 *   GET /v1/admin/usage/leaderboard          模型排行(按成本)
 * window 参数:正时长字符串(24h / 7d / 30d)。
 */
export interface OverviewTotals {
  requests: number
  total_cost: string
  total_tokens: number
  active_users: number
  active_api_keys: number
  success_count: number
  error_count: number
  success_rate: string
}

export interface OverviewTrendPoint {
  day: string
  requests: number
  cost: string
}

export interface OverviewResponse {
  window: string
  totals: OverviewTotals
  trend: OverviewTrendPoint[]
}

export interface PerfMetricsResponse {
  window: string
  model?: string | null
  summary: {
    avg_ttft_ms: string
    avg_tps: string
    request_count: number
    error_count: number
    error_rate: string
  }
  latency_percentiles_ms: { p50: number; p95: number; p99: number }
}

export interface LeaderboardEntry {
  rank: number
  key: string
  total_cost: string
  total_tokens: number
  request_count: number
}

export interface LeaderboardResponse {
  window: string
  by: string
  entries: LeaderboardEntry[]
}

export const OPS_WINDOWS: ReadonlyArray<{ value: string; label: string }> = [
  { value: '24h', label: '近 24 小时' },
  { value: '7d', label: '近 7 天' },
  { value: '30d', label: '近 30 天' },
]

/*
 * ── 用量性能分析(4 个新增只读端点)─────────────────────────────────────────────
 * 端点真实性(全 admin token 鉴权,只读):
 *   GET /v1/admin/usage/performance            性能排行(按 model / provider_account)
 *   GET /v1/admin/usage/perf-metrics/by-bucket 按小时/天的性能分桶
 *   GET /v1/admin/usage/health-score           用量(业务面)+ 渠道(基础设施面)综合健康分
 *   GET /v1/admin/usage/provider-account-counts 各 provider 账号的请求/Token/费用聚合
 * 见 backend/cmd/gateway/routes_usageadmin.go:25/29/31/35 +
 *    backend/internal/usageanalyticshttp/{performance_handler,perf_metrics_handler,provider_account_counts_handler}.go。
 * 注意:performance / health-score / perf-metrics 的 error_rate 是 0~1 的小数(fixed4),
 *       不是百分数;展示前须乘 100(见 ops.ts fmtFractionPct)。
 */

/** 性能维度:按模型 or 按 provider 账号。镜像后端 performanceByModel / leaderboardByProvider。 */
export type PerfDimension = 'model' | 'provider_account'

export const PERF_DIMENSIONS: ReadonlyArray<{ value: PerfDimension; label: string }> = [
  { value: 'model', label: '按模型' },
  { value: 'provider_account', label: '按 Provider 账号' },
]

export interface PerformanceEntry {
  rank: number
  key: string
  avg_ttft_ms: string
  avg_tps: string
  request_count: number
  error_rate: string
}

export interface PerformanceResponse {
  window: string
  by: PerfDimension
  entries: PerformanceEntry[]
}

/** 分桶粒度:小时 or 天。镜像后端 perfMetricsBucketHour / perfMetricsBucketDay。 */
export type PerfBucketGranularity = 'hour' | 'day'

export const PERF_BUCKETS: ReadonlyArray<{ value: PerfBucketGranularity; label: string }> = [
  { value: 'hour', label: '按小时' },
  { value: 'day', label: '按天' },
]

export interface PerfBucketEntry {
  bucket: string
  key: string
  avg_ttft_ms: string
  avg_tps: string
  request_count: number
  error_count: number
  error_rate: string
}

export interface PerfBucketResponse {
  window: string
  bucket: PerfBucketGranularity
  by: string
  entries: PerfBucketEntry[]
}

export interface HealthScoreResponse {
  window: string
  overall_score: number
  business_score: number
  infra_score: number
  signals: {
    error_rate: string
    ttft_p99_ms: number
    channel_health_available: boolean
    healthy_channels: number
    managed_channels: number
  }
}

export interface ProviderAccountCount {
  provider_account_id: number
  request_count: number
  total_input_tokens: number
  total_output_tokens: number
  total_cost: string
}

export interface ProviderAccountCountsResponse {
  from: string
  to: string
  counts: ProviderAccountCount[]
}
