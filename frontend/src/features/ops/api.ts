import { apiGet } from '../../lib/api'
import type {
  HealthScoreResponse,
  LeaderboardResponse,
  OverviewResponse,
  PerfBucketGranularity,
  PerfBucketResponse,
  PerfDimension,
  PerformanceResponse,
  PerfMetricsResponse,
  ProviderAccountCountsResponse,
} from './types'

/*
 * Ops 大屏数据访问层。端点 /v1/admin/usage/*(admin token 鉴权,只读)。
 * /v1/admin/* 前缀经 lib/api 的 tokenForPath 自动注入 admin Bearer,无需手传。
 */
export async function getOverview(window: string, signal?: AbortSignal): Promise<OverviewResponse> {
  return apiGet<OverviewResponse>('/v1/admin/usage/overview', { query: { window }, signal })
}

export async function getPerfMetrics(window: string, signal?: AbortSignal): Promise<PerfMetricsResponse> {
  return apiGet<PerfMetricsResponse>('/v1/admin/usage/perf-metrics/summary', { query: { window }, signal })
}

export async function getLeaderboard(window: string, signal?: AbortSignal): Promise<LeaderboardResponse> {
  return apiGet<LeaderboardResponse>('/v1/admin/usage/leaderboard', { query: { window }, signal })
}

/**
 * 性能排行(只读)。GET /v1/admin/usage/performance?by=&window=。
 * by ∈ {model, provider_account};后端默认 model,这里显式下发。
 * 见 routes_usageadmin.go:25 + performance_handler.go。
 */
export async function getPerformance(
  by: PerfDimension,
  window: string,
  signal?: AbortSignal,
): Promise<PerformanceResponse> {
  return apiGet<PerformanceResponse>('/v1/admin/usage/performance', { query: { by, window }, signal })
}

/**
 * 按时间桶的性能分布(只读)。GET /v1/admin/usage/perf-metrics/by-bucket?bucket=&window=。
 * bucket ∈ {hour, day}(后端只支持这两个,按 requested_at 分桶,固定 by=model)。
 * 见 routes_usageadmin.go:29 + perf_metrics_handler.go:125。
 */
export async function getPerfByBucket(
  bucket: PerfBucketGranularity,
  window: string,
  signal?: AbortSignal,
): Promise<PerfBucketResponse> {
  return apiGet<PerfBucketResponse>('/v1/admin/usage/perf-metrics/by-bucket', {
    query: { bucket, window },
    signal,
  })
}

/**
 * 用量 + 渠道健康综合分(只读,0~100)。GET /v1/admin/usage/health-score?window=。
 * 业务面=用户可见错误率 + TTFT p99;基础设施面=(可选)按 tenant_id 取渠道健康分布。
 * 运维大屏是平台级总览、不绑单一租户,故不传 tenant_id(后端 infra_score 走保守满分降级)。
 * 见 routes_usageadmin.go:31 + perf_metrics_handler.go:161。
 */
export async function getHealthScore(
  window: string,
  signal?: AbortSignal,
): Promise<HealthScoreResponse> {
  return apiGet<HealthScoreResponse>('/v1/admin/usage/health-score', { query: { window }, signal })
}

/**
 * 各 provider 账号的请求/Token/费用聚合(只读)。
 * GET /v1/admin/usage/provider-account-counts?from=&to=(RFC3339,必填,窗口 ≤90 天)。
 * 见 routes_usageadmin.go:35 + provider_account_counts_handler.go。
 */
export async function getProviderAccountCounts(
  from: string,
  to: string,
  signal?: AbortSignal,
): Promise<ProviderAccountCountsResponse> {
  return apiGet<ProviderAccountCountsResponse>('/v1/admin/usage/provider-account-counts', {
    query: { from, to },
    signal,
  })
}
