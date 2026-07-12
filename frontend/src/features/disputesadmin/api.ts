import { apiGet, apiSend } from '../../lib/api'
import { buildListQuery } from './disputes'
import type {
  DisputeFilters,
  DisputeListResponse,
  DisputeResolveRequest,
  DisputeResolveResponse,
} from './types'

/*
 * 退款/扣费争议台数据访问层。所有端点挂在 /v1/admin/disputes,经 tokenForPath 自动带 admin token。
 * 端点真实性见 backend/internal/controlhttp/dispute_handler.go + cmd/gateway/routes.go:1088-1091。
 * money 敏感:列表只读,resolve 是对一笔已计费请求的人工裁决(退款/维持),写动作须二次确认。
 */

/** 列争议。GET /v1/admin/disputes?tenant_id=N&status=&limit=&offset=(dispute_handler.go:139)。 */
export async function listDisputes(
  tenantId: number,
  filters: DisputeFilters,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<DisputeListResponse> {
  return apiGet<DisputeListResponse>('/v1/admin/disputes', {
    query: buildListQuery(tenantId, filters, limit, offset),
    signal,
  })
}

/**
 * 裁决争议(money 敏感,UI 须二次确认)。POST /v1/admin/disputes/{id}/resolve
 * body {tenant_id, status, operator_note}(dispute_handler.go:172)。
 * id 用 path 定位;tenant_id 在 body 里供后端 CanIssueForTenant 二次校验(dispute_handler.go:191)。
 */
export async function resolveDispute(
  id: number,
  body: DisputeResolveRequest,
): Promise<DisputeResolveResponse> {
  return apiSend<DisputeResolveResponse>('POST', `/v1/admin/disputes/${id}/resolve`, body)
}
