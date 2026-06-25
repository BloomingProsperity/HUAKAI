import { apiGet } from '../../lib/api'
import type { LeaderboardResponse, OverviewResponse, PerfMetricsResponse } from './types'

/*
 * Ops 大屏数据访问层。端点 /v1/admin/usage/*(admin token 鉴权,只读)。
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
