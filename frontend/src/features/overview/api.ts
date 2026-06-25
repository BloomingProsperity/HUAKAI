import { apiGet } from '../../lib/api'
import type { ApiKeyListResponse, KeyUsageSummary, QuotaResponse } from './types'

/*
 * 概览页数据访问层。全部走 session 鉴权(apiGet 按路径自动注入 session token):
 *   - /v1/me/quota                     配额窗口
 *   - /v1/me/keys/{id}/usage-summary   单个 Key 的用量汇总
 *   - /v1/api-keys                     当前用户名下的 Key 列表(取数量)
 * 这三个端点都已核 backend 真码确认挂在 SessionMiddleware 下(详见 types.ts 顶注)。
 */

export async function getQuota(signal?: AbortSignal): Promise<QuotaResponse> {
  return apiGet<QuotaResponse>('/v1/me/quota', { signal })
}

export async function listApiKeys(offset = 0, limit = 100, signal?: AbortSignal): Promise<ApiKeyListResponse> {
  return apiGet<ApiKeyListResponse>('/v1/api-keys', { query: { offset, limit }, signal })
}

export async function getKeyUsageSummary(keyId: number, signal?: AbortSignal): Promise<KeyUsageSummary> {
  return apiGet<KeyUsageSummary>(`/v1/me/keys/${keyId}/usage-summary`, { signal })
}
