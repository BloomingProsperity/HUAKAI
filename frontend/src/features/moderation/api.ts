import { apiGet, apiSend } from '../../lib/api'
import { buildConfigQuery, buildLogQuery } from './moderation'
import type {
  LogFilters,
  ModerationConfig,
  ModerationConfigUpdate,
  ModerationLogListResponse,
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
