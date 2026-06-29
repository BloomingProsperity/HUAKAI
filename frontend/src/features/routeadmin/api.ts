import { apiGet, apiSend } from '../../lib/api'
import type {
  CreateRouteRequest,
  Route,
  RouteListResponse,
  RouteResponse,
  UpdateRouteRequest,
} from './types'

/*
 * 请求路由规则(routes 表)数据访问层。所有端点挂在 /v1/admin/routes,经 tokenForPath
 * 自动注入 admin Bearer(/v1/admin 前缀)。仅 platform_admin 可访问。
 *
 * 端点真实性(backend file:line):
 *   - MountRouteAdminRoutes  → controlhttp/routeadmin_handler.go:104-111
 *   - 路由挂载前缀 /v1/admin/routes → cmd/gateway/routes.go:1098-1103
 * 租户参数:List/Get/Update/Delete/SetEnabled 经 query ?tenant_id=N(handler 用
 * routeAdminParsePositiveQuery 取,routeadmin_handler.go:145/163/190/229/260);
 * Create 经 body.tenant_id(createRouteRequest.TenantID,routeadmin_handler.go:47/124)。
 */

/** 列某租户的全部路由规则。GET /v1/admin/routes?tenant_id=N(handler:140-156)。 */
export async function listRoutes(tenantId: number, signal?: AbortSignal): Promise<Route[]> {
  const resp = await apiGet<RouteListResponse>('/v1/admin/routes', {
    query: { tenant_id: tenantId },
    signal,
  })
  return resp.routes ?? []
}

/** 取单条路由规则。GET /v1/admin/routes/{id}?tenant_id=N(handler:158-178)。 */
export async function getRoute(
  id: number,
  tenantId: number,
  signal?: AbortSignal,
): Promise<Route> {
  const resp = await apiGet<RouteResponse>(`/v1/admin/routes/${id}`, {
    query: { tenant_id: tenantId },
    signal,
  })
  return resp.route
}

/** 新建路由规则。POST /v1/admin/routes(tenant_id 在 body,201)(handler:113-138)。 */
export async function createRoute(body: CreateRouteRequest): Promise<Route> {
  const resp = await apiSend<RouteResponse>('POST', '/v1/admin/routes', body)
  return resp.route
}

/**
 * 全替换一条路由规则。PUT /v1/admin/routes/{id}?tenant_id=N(handler:184-218)。
 * body 不含 tenant_id(后端 DisallowUnknownFields 会拒);match_priority 始终显式带(全替换语义)。
 */
export async function updateRoute(
  id: number,
  tenantId: number,
  body: UpdateRouteRequest,
): Promise<Route> {
  const resp = await apiSend<RouteResponse>('PUT', `/v1/admin/routes/${id}`, body, {
    query: { tenant_id: tenantId },
  })
  return resp.route
}

/**
 * 启停一条路由规则(改动型,UI 二次确认)。PUT /v1/admin/routes/{id}/enabled?tenant_id=N
 * {enabled}(handler:223-252)。enabled 必须显式给(后端 *bool,缺字段 400)。
 */
export async function setRouteEnabled(
  id: number,
  tenantId: number,
  enabled: boolean,
): Promise<Route> {
  const resp = await apiSend<RouteResponse>(
    'PUT',
    `/v1/admin/routes/${id}/enabled`,
    { enabled },
    { query: { tenant_id: tenantId } },
  )
  return resp.route
}

/** 软删一条路由规则(破坏性,UI 二次确认)。DELETE /v1/admin/routes/{id}?tenant_id=N(handler:254-275)。 */
export async function deleteRoute(id: number, tenantId: number): Promise<Route> {
  const resp = await apiSend<RouteResponse>('DELETE', `/v1/admin/routes/${id}`, undefined, {
    query: { tenant_id: tenantId },
  })
  return resp.route
}
