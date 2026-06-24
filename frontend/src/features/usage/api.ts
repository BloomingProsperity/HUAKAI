import { apiGet } from '../../lib/api'
import type { KeyUsageSummary, QuotaResponse } from './types'

/*
 * 用量与配额数据访问层(session 鉴权 /v1/me/*)。
 */
export async function getQuota(signal?: AbortSignal): Promise<QuotaResponse> {
  return apiGet<QuotaResponse>('/v1/me/quota', { signal })
}

export async function getKeyUsageSummary(keyId: number, signal?: AbortSignal): Promise<KeyUsageSummary> {
  return apiGet<KeyUsageSummary>(`/v1/me/keys/${keyId}/usage-summary`, { signal })
}
