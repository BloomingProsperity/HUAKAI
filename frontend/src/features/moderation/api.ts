import { apiGet, apiSend } from '../../lib/api'
import { buildConfigQuery, buildLogQuery } from './moderation'
import type {
  BannedAPIKeyListResponse,
  BulkCreateResult,
  HashCreateRequest,
  HashListResponse,
  HashRule,
  KeywordCreateRequest,
  KeywordListResponse,
  KeywordRule,
  LogFilters,
  ModerationConfig,
  ModerationConfigUpdate,
  ModerationLogListResponse,
  UnbanAPIKeyResult,
} from './types'

/*
 * 内容审核(风控)数据访问层。所有端点挂在 /admin/v1/moderation,经 tokenForPath 带 admin token。
 * 端点真实性见 backend/internal/moderationhttp/mount.go + cmd/gateway/routes.go:1094。
 */

/** 取某租户的审核配置。GET /admin/v1/moderation/config?tenant_id=N。 */
export async function getModerationConfig(
  tenantId: number,
  signal?: AbortSignal,
): Promise<ModerationConfig> {
  return apiGet<ModerationConfig>('/admin/v1/moderation/config', {
    query: buildConfigQuery(tenantId),
    signal,
  })
}

/** upsert 某租户的审核配置。PUT /admin/v1/moderation/config(tenant_id 在 body)。 */
export async function updateModerationConfig(
  body: ModerationConfigUpdate,
): Promise<ModerationConfig> {
  return apiSend<ModerationConfig>('PUT', '/admin/v1/moderation/config', body)
}

/** 列命中日志(只读)。GET /admin/v1/moderation/logs?tenant_id=N&api_key_id=&limit=&offset=。 */
export async function listModerationLogs(
  tenantId: number,
  filters: LogFilters,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<ModerationLogListResponse> {
  return apiGet<ModerationLogListResponse>('/admin/v1/moderation/logs', {
    query: buildLogQuery(tenantId, filters, limit, offset),
    signal,
  })
}

// ── 关键词黑名单 CRUD(mount.go:39-42)──────────────────────────────────────────

/** 列关键词。GET /keywords?tenant_id=N&limit=&offset=。 */
export async function listKeywords(
  tenantId: number,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<KeywordListResponse> {
  return apiGet<KeywordListResponse>('/admin/v1/moderation/keywords', {
    query: { tenant_id: tenantId, limit, offset },
    signal,
  })
}

/** 新建关键词。POST /keywords(tenant_id 在 body)。 */
export async function createKeyword(body: KeywordCreateRequest): Promise<KeywordRule> {
  return apiSend<KeywordRule>('POST', '/admin/v1/moderation/keywords', body)
}

/** 批量导入关键词(≤1000)。POST /keywords/bulk {tenant_id, items[]}。 */
export async function bulkCreateKeywords(
  tenantId: number,
  items: Array<{ keyword: string; reason_code: string; enabled?: boolean }>,
): Promise<BulkCreateResult> {
  return apiSend<BulkCreateResult>('POST', '/admin/v1/moderation/keywords/bulk', {
    tenant_id: tenantId,
    items,
  })
}

/** 删除关键词。DELETE /keywords/{id}?tenant_id=N(204 无 body)。 */
export async function deleteKeyword(id: number, tenantId: number): Promise<void> {
  await apiSend<void>('DELETE', `/admin/v1/moderation/keywords/${id}`, undefined, {
    query: { tenant_id: tenantId },
  })
}

// ── 哈希黑名单 CRUD(mount.go:43-46)────────────────────────────────────────────

/** 列哈希。GET /hashes?tenant_id=N&limit=&offset=。 */
export async function listHashes(
  tenantId: number,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<HashListResponse> {
  return apiGet<HashListResponse>('/admin/v1/moderation/hashes', {
    query: { tenant_id: tenantId, limit, offset },
    signal,
  })
}

/** 新建哈希。POST /hashes(hash_hex 须 64 位小写 hex,前端已统一转小写)。 */
export async function createHash(body: HashCreateRequest): Promise<HashRule> {
  return apiSend<HashRule>('POST', '/admin/v1/moderation/hashes', body)
}

/** 批量导入哈希(≤1000)。POST /hashes/bulk {tenant_id, items[]}。 */
export async function bulkCreateHashes(
  tenantId: number,
  items: Array<{ hash_hex: string; reason_code: string; enabled?: boolean }>,
): Promise<BulkCreateResult> {
  return apiSend<BulkCreateResult>('POST', '/admin/v1/moderation/hashes/bulk', {
    tenant_id: tenantId,
    items,
  })
}

/** 删除哈希。DELETE /hashes/{id}?tenant_id=N(204 无 body)。 */
export async function deleteHash(id: number, tenantId: number): Promise<void> {
  await apiSend<void>('DELETE', `/admin/v1/moderation/hashes/${id}`, undefined, {
    query: { tenant_id: tenantId },
  })
}

// ── 被封 Key 列表 + 解封(mount.go:50-51)──────────────────────────────────────

/** 列被封 Key。GET /banned?tenant_id=N&limit=&offset=。 */
export async function listBannedAPIKeys(
  tenantId: number,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<BannedAPIKeyListResponse> {
  return apiGet<BannedAPIKeyListResponse>('/admin/v1/moderation/banned', {
    query: { tenant_id: tenantId, limit, offset },
    signal,
  })
}

/** 解封 API Key(破坏性/恢复服务,UI 须二次确认)。POST /api-keys/{id}/unban {tenant_id, reason}。 */
export async function unbanAPIKey(
  apiKeyId: number,
  tenantId: number,
  reason: string,
): Promise<UnbanAPIKeyResult> {
  return apiSend<UnbanAPIKeyResult>('POST', `/admin/v1/moderation/api-keys/${apiKeyId}/unban`, {
    tenant_id: tenantId,
    reason,
  })
}
