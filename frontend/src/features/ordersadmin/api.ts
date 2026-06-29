import { getTokens } from '../../auth/store'
import { ApiError, apiGet, apiSend } from '../../lib/api'
import type {
  AdminOrder,
  DashboardStats,
  OrderAuditResponse,
  OrderDetailResponse,
  OrderListResponse,
  RefundOrderResponse,
  RefundRequestDecisionResponse,
  RefundRequestListResponse,
} from './types'

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

/**
 * 订单退款(money 敏感,破坏性:把已到账充值退回上游+扣减用户余额)。
 * POST /{id}/refund {tenant_id, amount_cents, idempotency_key, reason?}(refund.go:12 refundRequest)。
 * 后端要求 amount_cents ∈ (0, 订单原额](store_postgres_refund.go:64),**无 0=全额兜底**;
 * 故 amountCents 必须为正(全额退由调用方用订单原额算出,见 parseRefundAmount)。
 * idempotency_key 用于幂等去重(同 key 重复请求不重复退款)。
 */
export async function refundOrder(
  id: number,
  tenantId: number,
  amountCents: number,
  idempotencyKey: string,
  reason: string,
): Promise<RefundOrderResponse> {
  const body: {
    tenant_id: number
    amount_cents: number
    idempotency_key: string
    reason?: string
  } = { tenant_id: tenantId, amount_cents: amountCents, idempotency_key: idempotencyKey }
  if (reason.trim()) body.reason = reason.trim()
  return apiSend<RefundOrderResponse>('POST', `${PATH}/${id}/refund`, body)
}

/** 订单审计轨迹(独立端点):GET /{id}/audit?tenant_id=(admin_panel.go:84)。 */
export async function getOrderAudit(
  id: number,
  tenantId: number,
  signal?: AbortSignal,
): Promise<OrderAuditResponse> {
  return apiGet<OrderAuditResponse>(`${PATH}/${id}/audit`, {
    query: { tenant_id: tenantId },
    signal,
  })
}

// ── 退款工单(用户发起、待 admin 审批)─────────────────────────────────────

/** 列待审批退款工单:GET /refund-requests?tenant_id=(refund_request_admin.go:34)。 */
export async function listRefundRequests(
  tenantId: number,
  signal?: AbortSignal,
): Promise<RefundRequestListResponse> {
  return apiGet<RefundRequestListResponse>(`${PATH}/refund-requests`, {
    query: { tenant_id: tenantId },
    signal,
  })
}

/**
 * 通过退款工单(money 敏感:approve 会以订单原额走 RefundOrder 资金路径,扣减用户余额)。
 * POST /refund-requests/{id}/approve {tenant_id}(refund_request_admin.go:57)。
 */
export async function approveRefundRequest(
  id: number,
  tenantId: number,
): Promise<RefundRequestDecisionResponse> {
  return apiSend<RefundRequestDecisionResponse>(
    'POST',
    `${PATH}/refund-requests/${id}/approve`,
    { tenant_id: tenantId },
  )
}

/**
 * 驳回退款工单(不动钱,仅置状态 rejected)。
 * POST /refund-requests/{id}/reject {tenant_id, reason?}(refund_request_admin.go:85)。
 */
export async function rejectRefundRequest(
  id: number,
  tenantId: number,
  reason: string,
): Promise<RefundRequestDecisionResponse> {
  const body: { tenant_id: number; reason?: string } = { tenant_id: tenantId }
  if (reason.trim()) body.reason = reason.trim()
  return apiSend<RefundRequestDecisionResponse>(
    'POST',
    `${PATH}/refund-requests/${id}/reject`,
    body,
  )
}

// ── 仪表盘 ─────────────────────────────────────────────────────────────────

/** 订单台仪表盘汇总:GET /dashboard?tenant_id=&created_from=&created_to=(admin_panel.go:66)。 */
export async function getDashboard(
  tenantId: number,
  signal?: AbortSignal,
  createdFrom?: string,
  createdTo?: string,
): Promise<DashboardStats> {
  return apiGet<DashboardStats>(`${PATH}/dashboard`, {
    query: { tenant_id: tenantId, created_from: createdFrom, created_to: createdTo },
    signal,
  })
}

// ── CSV 导出(blob 下载,走 admin token)────────────────────────────────────

/**
 * 导出类型 → 后端 CSV 端点路径(exporthttp/export.go:69-73 MountRoutes 真路由)。
 * 注意:这四个端点【不接受 tenant_id query】,租户由 admin 凭据 ScopeTenantID 推导
 * (export.go:218 resolveTenantScope);from/to 为必填 RFC3339,status 仅 payments/orders 接受
 * (refunds 与 usage 都不消费 status)。usage 用量明细导出真码 export.go:141 NewUsageExportHandler,
 * 表头 export.go:32 = request_id/user_id/model/tokens_input/tokens_output/cost_usd/created_at。
 */
const EXPORT_PATHS = {
  payments: '/v1/admin/payments/export.csv',
  orders: '/v1/admin/orders/export.csv',
  refunds: '/v1/admin/refunds/export.csv',
  usage: '/v1/admin/usage/export.csv',
} as const

export type ExportKind = keyof typeof EXPORT_PATHS

/**
 * 下载 CSV(blob)。CSV 端点返回 text/csv 非 JSON,且 apiGet 会按 JSON 解析,
 * 故此处走裸 fetch 并【显式注入 admin Bearer】(同 accounts/createApi 的混合渠道处理:
 * 裸 fetch 不经 lib/api 的 tokenForPath 注入,必须手动带头,否则恒 401)。
 * 成功后触发浏览器另存。token 缺失时不带头(后端自然 401),错误归一化成 ApiError。
 */
export async function downloadExportCsv(
  kind: ExportKind,
  from: string,
  to: string,
  status?: string,
): Promise<void> {
  const params = new URLSearchParams({ from, to })
  // 仅 payments/orders 消费 status(orders_refunds_export.go);refunds 与 usage 均不读 status,带了也无害但留空更干净。
  if (status && status.trim() && (kind === 'payments' || kind === 'orders')) params.set('status', status.trim())
  const path = `${EXPORT_PATHS[kind]}?${params.toString()}`
  const token = getTokens().adminToken
  const resp = await fetch(path, {
    method: 'GET',
    credentials: 'include',
    headers: {
      Accept: 'text/csv',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
  })
  if (!resp.ok) {
    // 错误体是 JSON {error:{code,message}};尽力解析,失败则回落状态文案。
    let code = `http_${resp.status}`
    let message = resp.statusText || '导出失败'
    try {
      const body = (await resp.json()) as { error?: { code?: string; message?: string } }
      if (body?.error) {
        code = body.error.code || code
        message = body.error.message || message
      }
    } catch {
      /* 非 JSON 错误体,保留回落文案 */
    }
    throw new ApiError(resp.status, code, message)
  }
  const blob = await resp.blob()
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement('a')
    a.href = url
    a.download = `${kind}-export.csv`
    document.body.appendChild(a)
    a.click()
    a.remove()
  } finally {
    URL.revokeObjectURL(url)
  }
}
