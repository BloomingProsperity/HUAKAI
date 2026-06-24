/*
 * API Key(用户态)前端类型 —— 镜像 userkeyhttp 的 JSON 形态。
 * 端点:/v1/api-keys(session 鉴权,用户管理自己名下的 key)。
 */

/** 列表/详情项(apiKeyView)。 */
export interface ApiKeyView {
  api_key_id: number
  name: string
  key_prefix: string
  status: string
  expires_at?: string | null
  last_used_at?: string | null
  revoked_at?: string | null
  revoked_reason?: string
  created_at: string
  updated_at: string
}

export interface ApiKeyListResponse {
  api_keys: ApiKeyView[]
  count: number
}

export interface CreateKeyRequest {
  name: string
  environment?: string
  /** RFC3339;省略=永不过期。 */
  expires_at?: string
}

/** 创建响应:plaintext 仅此一次出现,关闭后只回 key_prefix。 */
export interface CreateKeyResponse {
  api_key_id: number
  plaintext: string
  key_prefix: string
  status: string
  expires_at?: string | null
  created_at: string
  notice: string
}

export interface RevokeResponse {
  api_key_id: number
  already_revoked: boolean
}
