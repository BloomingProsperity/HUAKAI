import { apiGet } from '../../lib/api'
import type { RiskOverview } from './types'

/*
 * 风控只读总览数据访问层。端点 /admin/v1/risk/overview 走 apiGet
 * → authHeaders(path) 按 /admin/ 前缀自动注入 admin token(不裸 fetch,避 #143 漏鉴权坑)。
 * 真码:backend/internal/riskoverviewhttp/handler.go(MountAdminRoutes)、
 *       backend/cmd/gateway/routes_risk.go(mountRiskAdminRoutes,admin 鉴权 + 强 tenant 隔离)。
 * 平台 admin 必须传 tenant_id;租户运营者后端回退自身 scope。
 */

const OVERVIEW = '/admin/v1/risk/overview'

/** 取某租户的风控信号计数快照(只读)。 */
export async function getRiskOverview(tenantId: number, signal?: AbortSignal): Promise<RiskOverview> {
  return apiGet<RiskOverview>(OVERVIEW, { query: { tenant_id: tenantId }, signal })
}
