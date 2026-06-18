// 用户自助 API Key 管理：封装 /v1/api-keys/* 端点。
// 字段形状对齐后端 internal/userkeyhttp/handlers.go（session 鉴权，走 userClient）。
// 单 key 用量摘要走 /v1/me/keys/{id}/usage-summary
// （internal/usageanalyticshttp/key_summary_handler.go，同样 session 鉴权）。
import { userGet, userPost, userPatch, userDelete } from './userClient';

// ---- 后端响应类型（snake_case，与 handler JSON 一致） ----

export type ApiKeyStatus = 'active' | 'revoked' | (string & {});
export type ApiKeyEnvironment = 'live' | 'test';

// GET /v1/api-keys 列表项（handlers.go apiKeyView）
export interface ApiKeyView {
  api_key_id: number;
  name: string;
  key_prefix: string;
  status: ApiKeyStatus;
  expires_at?: string | null;
  last_used_at?: string | null;
  revoked_at?: string | null;
  revoked_reason?: string;
  created_at: string;
  updated_at: string;
}

// GET /v1/api-keys 响应信封（handlers.go listResponse）
export interface ApiKeyListResponse {
  api_keys: ApiKeyView[];
  count: number;
}

// POST /v1/api-keys 请求体（handlers.go createRequest）
export interface CreateApiKeyRequest {
  name: string;
  environment?: ApiKeyEnvironment;
  // RFC3339；省略或空 = 永不过期
  expires_at?: string;
}

// POST /v1/api-keys 响应（handlers.go createResponse）——含一次性明文 plaintext
export interface CreateApiKeyResponse {
  api_key_id: number;
  plaintext: string;
  key_prefix: string;
  status: ApiKeyStatus;
  expires_at?: string | null;
  created_at: string;
  notice: string;
}

// DELETE /v1/api-keys/{id} 响应（handlers.go revokeResponse）
export interface RevokeApiKeyResponse {
  api_key_id: number;
  already_revoked: boolean;
}

const BASE_PATH = '/v1/api-keys';

// 列出当前用户的 key。后端支持 offset/limit 分页（默认即可，先不暴露分页 UI）。
export function listApiKeys(opts?: { offset?: number; limit?: number }): Promise<ApiKeyListResponse> {
  return userGet<ApiKeyListResponse>(BASE_PATH, {
    offset: opts?.offset,
    limit: opts?.limit,
  });
}

// 创建 key。后端只接受 name（必填）+ environment + expires_at；其余可选项后端未暴露。
export function createApiKey(req: CreateApiKeyRequest): Promise<CreateApiKeyResponse> {
  const body: CreateApiKeyRequest = { name: req.name };
  if (req.environment) body.environment = req.environment;
  if (req.expires_at) body.expires_at = req.expires_at;
  return userPost<CreateApiKeyResponse>(BASE_PATH, body);
}

// PATCH /v1/api-keys/{id} 部分更新（handlers.go patchRequest）。所有字段可选。
// expires_at 三态：省略=不改有效期、""=清成永不过期、RFC3339=设新有效期（详见 api-key-expiry-form.ts）。
export interface UpdateApiKeyRequest {
  name?: string;
  status?: ApiKeyStatus;
  expires_at?: string;
}

// PATCH 响应（handlers.go patchResponse）。expires_at 缺省 = 永不过期。
export interface UpdateApiKeyResponse {
  api_key_id: number;
  name: string;
  status: ApiKeyStatus;
  expires_at?: string | null;
}

// 部分更新当前用户自己的一条 key（含延长/调整/清除有效期）。归属由后端 session + (id,tenant,user) 强制。
export function updateApiKey(id: number, patch: UpdateApiKeyRequest): Promise<UpdateApiKeyResponse> {
  return userPatch<UpdateApiKeyResponse>(`${BASE_PATH}/${id}`, patch);
}

// 撤销 key（后端用 DELETE，软删置 status=revoked，幂等）。reason 可选。
export function revokeApiKey(id: number, reason?: string): Promise<RevokeApiKeyResponse> {
  const trimmed = reason?.trim();
  const body = trimmed ? { reason: trimmed } : undefined;
  return userDelete<RevokeApiKeyResponse>(`${BASE_PATH}/${id}`, body);
}

// ---- 单 key 用量摘要 GET /v1/me/keys/{id}/usage-summary ----
// 与 /v1/api-keys 不同前缀；session 鉴权一致。非本人 key → 404。

// key_summary_handler.go keyUsageSummaryResponse（cost 为字符串十进制，token 计数为整型）
export interface KeyUsageSummary {
  api_key_id: number;
  total_cost: string;
  total_tokens_input: number;
  total_tokens_output: number;
  total_cache_read_tokens: number;
  total_cache_creation_tokens: number;
  request_count: number;
  from: string | null;
  to: string | null;
}

// 取某 key 的累计用量。可选 from/to（RFC3339）窗口，缺省即全量。
export function getKeyUsageSummary(
  id: number,
  window?: { from?: string; to?: string },
): Promise<KeyUsageSummary> {
  return userGet<KeyUsageSummary>(`/v1/me/keys/${id}/usage-summary`, {
    from: window?.from,
    to: window?.to,
  });
}
