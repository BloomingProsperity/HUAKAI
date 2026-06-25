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
