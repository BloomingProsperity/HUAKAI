import { apiGet } from '../../lib/api'
import { buildClaimQuery, buildUsageQuery } from './billingadmin'
import type { ClaimFilters, ClaimListResponse, UsageFilters, UsageListResponse } from './types'

/*
 * 管理员计费运营观测数据访问层(只读)。两端点均挂在 /admin/v1 下,经 tokenForPath 自动带 admin Bearer。
 * 端点真实性:
 *   - GET /admin/v1/usage          backend/cmd/gateway/routes.go:1113 → gatewayhttp.NewUsageHandler
 *                                  (admin_observability_handler.go:66)
 *   - GET /admin/v1/billing/claims backend/cmd/gateway/routes.go:1114 → gatewayhttp.NewClaimsHandler
 *                                  (admin_observability_handler.go:70)
 * 游标分页:首页不带 cursor,后续传上一页 next_cursor;limit ∈ [1,200]。纯只读,无任何写动作。
 */

/**
 * 列原始逐笔用量成本记录。GET /admin/v1/usage。
 * 过滤:provider/model/pool_id/api_key_id/provider_account_id/outcome/pending_reconciliation_only/from/to。
 */
export async function listUsageRecords(
  filters: UsageFilters,
  cursor?: string,
  limit = 100,
  signal?: AbortSignal,
): Promise<UsageListResponse> {
  return apiGet<UsageListResponse>('/admin/v1/usage', {
    query: { ...buildUsageQuery(filters, cursor), limit },
    signal,
  })
}

/**
 * 列计费 claim 台账记录。GET /admin/v1/billing/claims。
 * 过滤:status/provider/model/pool_id/api_key_id/provider_account_id/from/to。
 */
export async function listBillingClaims(
  filters: ClaimFilters,
  cursor?: string,
  limit = 100,
  signal?: AbortSignal,
): Promise<ClaimListResponse> {
  return apiGet<ClaimListResponse>('/admin/v1/billing/claims', {
    query: { ...buildClaimQuery(filters, cursor), limit },
    signal,
  })
}
