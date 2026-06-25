import { apiGet, apiSend } from '../../lib/api'
import type { AdminOrder, OrderDetailResponse, OrderListResponse } from './types'

/*
 * 订单管理台数据访问层。端点前缀 /v1/admin/payments(admin token 鉴权,经 tokenForPath)。
 * 真码:cmd/gateway/routes.go:1033 r.Route("/v1/admin/payments") + paymenthttp/handler.go:189
 *       MountPaymentAdminRoutes。所有读写仅接已有 admin 端点,不造任何支付。
 */
const PATH = '/v1/admin/payments'

/** 订单列表(多维筛选 + 分页)。query 由纯逻辑 buildOrderListQuery 构造(tenant_id 必填)。 */
export async function listOrders(
  query: Record<string, string | number | undefined>,
  signal?: AbortSignal,
): Promise<OrderListResponse> {
  // 注:列表挂在 "/" 上(handler.go:191 r.Get("/")),故路径带尾斜杠。
  return apiGet<OrderListResponse>(`${PATH}/`, { query, signal })
}

/** 订单详情 + 审计轨迹:GET /{id}?tenant_id=。tenant_id 必填(parsePositiveQuery)。 */
export async function getOrder(
  id: number,
  tenantId: number,
  signal?: AbortSignal,
): Promise<OrderDetailResponse> {
  return apiGet<OrderDetailResponse>(`${PATH}/${id}`, { query: { tenant_id: tenantId }, signal })
}

/** 确认订单(确认已支付并履约):POST /{id}/confirm {tenant_id, confirm_reason}。 */
export async function confirmOrder(
  id: number,
  tenantId: number,
  confirmReason: string,
): Promise<{ order: AdminOrder }> {
  const body: { tenant_id: number; confirm_reason?: string } = { tenant_id: tenantId }
  if (confirmReason.trim()) body.confirm_reason = confirmReason.trim()
  return apiSend<{ order: AdminOrder }>('POST', `${PATH}/${id}/confirm`, body)
}

/** 取消订单(运营撤单,仅 pending):POST /{id}/cancel {tenant_id, reason}。 */
export async function cancelOrder(
  id: number,
  tenantId: number,
  reason: string,
): Promise<{ order: AdminOrder }> {
  const body: { tenant_id: number; reason?: string } = { tenant_id: tenantId }
  if (reason.trim()) body.reason = reason.trim()
  return apiSend<{ order: AdminOrder }>('POST', `${PATH}/${id}/cancel`, body)
}

/** 重试履约(卡单恢复,仅 paid/recharging):POST /{id}/retry {tenant_id}。 */
export async function retryOrder(id: number, tenantId: number): Promise<{ order: AdminOrder }> {
  return apiSend<{ order: AdminOrder }>('POST', `${PATH}/${id}/retry`, { tenant_id: tenantId })
}
