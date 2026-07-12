import { apiGet } from '../../lib/api'
import type {
  KeyUsageGranularity,
  KeyUsageRecord,
  KeyUsageRecordsResponse,
  KeyUsageSummary,
  KeyUsageTimeSeriesResponse,
  QuotaResponse,
} from './types'

/* 用量数据访问层：汇总/配额走 session，Key 深度端点显式走 API Key Bearer。 */
export async function getQuota(signal?: AbortSignal): Promise<QuotaResponse> {
  return apiGet<QuotaResponse>('/v1/me/quota', { signal })
}

export async function getKeyUsageSummary(keyId: number, signal?: AbortSignal): Promise<KeyUsageSummary> {
  return apiGet<KeyUsageSummary>(`/v1/me/keys/${keyId}/usage-summary`, { signal })
}

export interface ListKeyUsageQuery {
  limit?: number
  cursor?: string
  from?: string
  to?: string
  model?: string
  provider?: string
  status?: 'success' | 'error'
}

/** API Key 自查逐笔用量；显式 bearer 防止统一客户端误用 session token。 */
export async function listKeyUsageRecords(
  apiKey: string,
  query: ListKeyUsageQuery = {},
  signal?: AbortSignal,
): Promise<KeyUsageRecordsResponse> {
  return apiGet<KeyUsageRecordsResponse>('/v1/me/usage', {
    bearer: apiKey.trim(),
    query: {
      limit: query.limit ?? 50,
      cursor: query.cursor,
      from: query.from,
      to: query.to,
      model: query.model,
      provider: query.provider,
      status: query.status,
    },
    signal,
  })
}

export interface KeyUsageTimeSeriesQuery {
  from: string
  to: string
  granularity: KeyUsageGranularity
}

/** API Key 自查时间序列；from/to 必填且由 UI 保证不超过 31 天。 */
export async function getKeyUsageTimeSeries(
  apiKey: string,
  query: KeyUsageTimeSeriesQuery,
  signal?: AbortSignal,
): Promise<KeyUsageTimeSeriesResponse> {
  return apiGet<KeyUsageTimeSeriesResponse>('/v1/me/analytics/time-series', {
    bearer: apiKey.trim(),
    query: { from: query.from, to: query.to, granularity: query.granularity },
    signal,
  })
}

/** 在当前 API Key + 用户身份作用域内按 request_id 查询单笔。 */
export async function getKeyGeneration(
  apiKey: string,
  requestId: string,
  signal?: AbortSignal,
): Promise<KeyUsageRecord> {
  return apiGet<KeyUsageRecord>('/v1/generation', {
    bearer: apiKey.trim(),
    query: { id: requestId.trim() },
    signal,
  })
}
