import { apiGet, apiSend } from '../../lib/api'
import type { ApiKeyListResponse, CreateKeyRequest, CreateKeyResponse, RevokeResponse } from './types'

/*
 * API Key 数据访问层。端点 /v1/api-keys(session 鉴权,管理当前登录用户名下的 key)。
 */
const KEYS_PATH = '/v1/api-keys'

export async function listApiKeys(offset = 0, limit = 50, signal?: AbortSignal): Promise<ApiKeyListResponse> {
  return apiGet<ApiKeyListResponse>(KEYS_PATH, { query: { offset, limit }, signal })
}

/** 创建 Key。响应含一次性 plaintext(仅此一次,务必让用户立即保存)。 */
export async function createApiKey(body: CreateKeyRequest): Promise<CreateKeyResponse> {
  return apiSend<CreateKeyResponse>('POST', KEYS_PATH, body)
}

/** 撤销 Key(DELETE,可带原因)。already_revoked 表示幂等命中。 */
export async function revokeApiKey(id: number, reason: string): Promise<RevokeResponse> {
  return apiSend<RevokeResponse>('DELETE', `${KEYS_PATH}/${id}`, { reason: reason.trim() || undefined })
}

/** 编辑 Key(PATCH 改名 + 到期三态):仅下发改动字段。 */
export async function updateApiKey(id: number, body: object): Promise<unknown> {
  return apiSend<unknown>('PATCH', `${KEYS_PATH}/${id}`, body)
}
