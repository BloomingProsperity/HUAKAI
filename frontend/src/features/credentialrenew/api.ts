import { apiGet } from '../../lib/api'
import { buildRenewStatusQuery } from './renew'
import type { RenewStatusListResponse, RenewStatusQueryParams } from './types'

/*
 * 凭证续期监控数据访问层(只读)。
 *
 * 端点:GET /admin/v1/credentials/renew-status(游标分页)。
 * /admin/v1 前缀经 tokenForPath 自动注入 admin Bearer(见 lib/api.ts:74)。
 * 真实路由 + 响应结构见 backend/internal/gatewayhttp/admin_credentials_handler.go:79-127
 * + cmd/gateway/routes.go:993(挂载前缀 /admin/v1/credentials)。
 */

/**
 * 列凭证续期状态。GET /admin/v1/credentials/renew-status?limit=&cursor=&tenant_id=。
 * - limit 已在 buildRenewStatusQuery 钳进后端区间 [1,500]。
 * - 游标分页:响应 next_cursor 非 null 时,作为下一次调用的 cursor。
 * 角色范围由后端按 admin 身份裁定(platform_admin 看全部/指定租户,tenant_operator 仅本租户)。
 */
export async function listRenewStatus(
  params: RenewStatusQueryParams,
  signal?: AbortSignal,
): Promise<RenewStatusListResponse> {
  return apiGet<RenewStatusListResponse>('/admin/v1/credentials/renew-status', {
    query: buildRenewStatusQuery(params),
    signal,
  })
}
