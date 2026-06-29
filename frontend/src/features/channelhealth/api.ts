import { apiGet, apiSend } from '../../lib/api'
import { buildListQuery, buildTenantQuery } from './channelHealth'
import type {
  ChannelHealthDetailResponse,
  ChannelHealthItem,
  ChannelHealthListResponse,
  ChannelHealthOverrideRequest,
  ChannelHealthSummary,
  OverrideAction,
} from './types'

/*
 * 渠道健康台数据访问层。读端点挂 /v1/admin/channel-health,写端点挂
 * /v1/admin/provider-accounts/{id}/channel-health/*;两前缀均经 tokenForPath 注入 admin token。
 * 端点真实性见 backend/internal/gatewayhttp/channel_health_admin_handler.go
 * + cmd/gateway/routes.go:927-990。
 */

/** 列渠道健康。GET /v1/admin/channel-health?tenant_id=N&limit=&offset=(handler:60)。 */
export async function listChannelHealth(
  tenantId: number,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<ChannelHealthListResponse> {
  return apiGet<ChannelHealthListResponse>('/v1/admin/channel-health', {
    query: buildListQuery(tenantId, limit, offset),
    signal,
  })
}

/** 取状态聚合。GET /v1/admin/channel-health/summary?tenant_id=N(handler:90)。 */
export async function getChannelHealthSummary(
  tenantId: number,
  signal?: AbortSignal,
): Promise<ChannelHealthSummary> {
  return apiGet<ChannelHealthSummary>('/v1/admin/channel-health/summary', {
    query: buildTenantQuery(tenantId),
    signal,
  })
}

/** 取单渠道详情 + 审计事件。GET /v1/admin/channel-health/{channel_id}?tenant_id=N(handler:108)。 */
export async function getChannelHealthDetail(
  channelId: string,
  tenantId: number,
  signal?: AbortSignal,
): Promise<ChannelHealthDetailResponse> {
  return apiGet<ChannelHealthDetailResponse>(
    `/v1/admin/channel-health/${encodeURIComponent(channelId)}`,
    { query: buildTenantQuery(tenantId), signal },
  )
}

/**
 * 执行人工干预(pause/resume/force-active)。
 * POST /v1/admin/provider-accounts/{id}/channel-health/{action}(handler:48-51)。
 * {id}=provider_account_id;坐标(tenant_id/vendor/account_credential_id/credential_version)+ reason 在 body。
 * pause/force-active 是高影响动作(封停/绕过冷却),调用方须先二次确认。
 */
export async function channelHealthOverride(
  providerAccountId: number,
  action: OverrideAction,
  body: ChannelHealthOverrideRequest,
): Promise<ChannelHealthItem> {
  return apiSend<ChannelHealthItem>(
    'POST',
    `/v1/admin/provider-accounts/${providerAccountId}/channel-health/${action}`,
    body,
  )
}
