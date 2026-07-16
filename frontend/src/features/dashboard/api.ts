/* 运营总览一期只读数据访问层；admin 路径由 apiGet 自动选择 admin Bearer。 */
import { apiGet } from '../../lib/api'
import type { AccountHealthSummary } from '../accounts/types'
import type { AlertEventListResponse } from '../alerting/types'
import type { AuditListResponse } from '../audit/types'
import type { ChannelHealthSummary } from '../channelhealth/types'
import { listPools } from '../groups/api'
import { listPricing } from '../models/api'
import type { LeaderboardResponse, OverviewResponse } from '../ops/types'
import type { QuotaPolicyListResponse } from '../quotapolicies/types'

export function fetchUsageOverview(window: string, signal?: AbortSignal): Promise<OverviewResponse> {
  return apiGet<OverviewResponse>('/v1/admin/usage/overview', { query: { window }, signal })
}

export function fetchModelLeaderboard(window: string, signal?: AbortSignal): Promise<LeaderboardResponse> {
  return apiGet<LeaderboardResponse>('/v1/admin/usage/leaderboard', { query: { by: 'model', window, limit: 100 }, signal })
}

export function fetchAccountSummary(tenantId: number, signal?: AbortSignal): Promise<AccountHealthSummary> {
  return apiGet<AccountHealthSummary>('/admin/v1/provider-accounts/health-summary', { query: { tenant_id: tenantId }, signal })
}

export function fetchPoolSummary(tenantId: number, signal?: AbortSignal): Promise<ChannelHealthSummary> {
  return apiGet<ChannelHealthSummary>('/v1/admin/channel-health/summary', { query: { tenant_id: tenantId }, signal })
}

/** 账号池库存总数取管理列表，不能用尚未上报的健康汇总总数代替。 */
export async function fetchPoolInventoryCount(tenantId: number, signal?: AbortSignal): Promise<number> {
  const response = await listPools(tenantId, 200, signal)
  return response.items.length
}

export function fetchQuotaPolicies(tenantId: number, signal?: AbortSignal): Promise<QuotaPolicyListResponse> {
  return apiGet<QuotaPolicyListResponse>('/admin/v1/quota-policies', { query: { tenant_id: tenantId, limit: 200, offset: 0 }, signal })
}

/** 配额总数用于资源卡，必须遍历分页，不能把第一页行数冒充总量。 */
export async function fetchAllQuotaPolicies(tenantId: number, signal?: AbortSignal) {
  const items: QuotaPolicyListResponse['items'] = []
  const limit = 200
  for (let offset = 0; ; offset += limit) {
    const page = await apiGet<QuotaPolicyListResponse>('/admin/v1/quota-policies', { query: { tenant_id: tenantId, limit, offset }, signal })
    items.push(...page.items)
    if (page.items.length < limit) return items
  }
}

export function fetchFiringAlerts(tenantId: number, signal?: AbortSignal): Promise<AlertEventListResponse> {
  return apiGet<AlertEventListResponse>('/v1/admin/alert-events', { query: { tenant_id: tenantId, state: 'firing', limit: 200, offset: 0 }, signal })
}

/** firing 总数来自完整事件集；分页未尽时继续拉取，避免少报待办与角标。 */
export async function fetchAllFiringAlerts(tenantId: number, signal?: AbortSignal) {
  const items: AlertEventListResponse['items'] = []
  const limit = 200
  for (let offset = 0; ; offset += limit) {
    const page = await apiGet<AlertEventListResponse>('/v1/admin/alert-events', { query: { tenant_id: tenantId, state: 'firing', limit, offset }, signal })
    items.push(...page.items)
    if (page.items.length < limit) return items
  }
}

export function fetchRecentAuditEvents(tenantId: number, signal?: AbortSignal): Promise<AuditListResponse> {
  return apiGet<AuditListResponse>('/admin/v1/audit-events', { query: { tenant_id: tenantId, limit: 8 }, signal })
}

/** Operator 会话使用公开价目口径，避免依赖只接受 API Key 的模型目录。 */
export async function fetchPricingModelCount(signal?: AbortSignal): Promise<number> {
  const items = await listPricing(signal)
  return items.length
}
