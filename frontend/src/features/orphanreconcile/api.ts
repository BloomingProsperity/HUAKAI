import { apiGet, apiSend } from '../../lib/api'
import { buildListQuery } from './orphanreconcile'
import type { OrphanListResponse, ReconcileRequest, ReconcileResponse } from './types'

/*
 * 媒体任务孤儿对账数据访问层。两个端点都挂在 /admin/v1,经 tokenForPath 自动带 admin Bearer。
 * 端点真实性见 backend/cmd/gateway/routes.go:914-915 + internal/orphanreconcilehttp/{routes.go,reconcile.go}。
 *
 * **money 敏感**:reconcile 当 back_charge=true 时走 billing.Capture 追扣用户余额。
 */

/**
 * 列 pending 孤儿。GET /admin/v1/media-task-orphans?tenant_id=&limit=。
 * tenant_id 可选(缺省=platform_admin 跨租户全局扫);limit 仅在正整数时下发。
 */
export async function listOrphans(
  tenantId: number | null,
  limit: number,
  signal?: AbortSignal,
): Promise<OrphanListResponse> {
  return apiGet<OrphanListResponse>('/admin/v1/media-task-orphans', {
    query: buildListQuery(tenantId, limit),
    signal,
  })
}

/**
 * 对账一个孤儿。POST /admin/v1/media-task-orphans/{id}/reconcile。
 * **money**:body.back_charge=true 时真实追扣余额——调用方务必已做二次确认。
 * 注意后端在「请求追扣但未扣到」时返回 409(reconcile.go:90),lib/api 会抛 ApiError,
 * 调用方据 ApiError.status===409 解读为「未追扣、孤儿保持 pending」。
 */
export async function reconcileOrphan(
  id: number,
  body: ReconcileRequest,
): Promise<ReconcileResponse> {
  return apiSend<ReconcileResponse>(
    'POST',
    `/admin/v1/media-task-orphans/${id}/reconcile`,
    body,
  )
}
