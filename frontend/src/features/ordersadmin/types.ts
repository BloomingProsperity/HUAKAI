/*
 * 订单管理台(运营台)前端类型 —— 镜像 paymenthttp 的 admin JSON DTO。
 * 端点前缀:/v1/admin/payments(admin token 鉴权,经 tokenForPath)。
 *
 * 形态来源(真码):
 *   - adminOrderView      backend/internal/paymenthttp/handler.go:83
 *   - auditEventView      backend/internal/paymenthttp/handler.go:129
 * money 字段一律以后端给的 *_cents(int)为准,前端只读展示不在此动钱。
 */

/** 管理员视角订单 DTO,继承用户视角字段并附管理字段。 */
export interface AdminOrder {
  id: number
  out_trade_no: string
  user_id: number
  amount_cents: number
  currency_code: string
  status: string
  provider_kind: string
  order_kind: string
  subscription_plan_id?: number
  created_at: string
  updated_at: string
  expires_at?: string
  paid_at?: string
  completed_at?: string
  // —— 以下为 adminOrderView 额外暴露的管理字段(omitempty,可能缺省)——
  recharging_at?: string
  failed_at?: string
  created_by_admin_id?: number
  confirmed_by_admin_id?: number
  confirm_reason?: string
  provider_order_ref?: string
  failure_code?: string
  failure_message?: string
}

/** 订单审计事件(轨迹)。 */
export interface OrderAuditEvent {
  event_type: string
  actor_kind: string
  actor_id?: number
  reason_class?: string
  occurred_at: string
}

/** 列表响应:GET /v1/admin/payments/ → { orders }。 */
export interface OrderListResponse {
  orders: AdminOrder[]
}

/** 详情响应:GET /v1/admin/payments/{id} → { order, audit_events }。 */
export interface OrderDetailResponse {
  order: AdminOrder
  audit_events: OrderAuditEvent[]
}

/** 审计端点响应:GET /v1/admin/payments/{id}/audit → { audit_events }。 */
export interface OrderAuditResponse {
  audit_events: OrderAuditEvent[]
}

/**
 * 退款记录 DTO,镜像 paymenthttp refund.go:19 refundView。
 * money 字段以 *_cents(int)为准。
 */
export interface RefundView {
  id: number
  amount_cents: number
  currency_code: string
  idempotency_key: string
  reason?: string
  billing_event_id?: number
  created_at: string
}

/** 退款响应:POST /v1/admin/payments/{id}/refund → { order, refund, balance_cents, idempotent }。 */
export interface RefundOrderResponse {
  order: AdminOrder
  refund: RefundView
  balance_cents: number
  idempotent: boolean
}

/**
 * 退款工单 DTO,镜像 paymenthttp user_portal.go:226 refundRequestView。
 * 这是用户发起、待 admin 审批的退款申请(本身不含资金事实;approve 才走资金路径)。
 */
export interface RefundRequestView {
  id: number
  tenant_id?: number
  user_id?: number
  order_id: number
  status: string
  reason?: string
  created_at: string
  decided_at?: string
  decided_by?: number
}

/** 退款工单列表响应:GET /v1/admin/payments/refund-requests → { refund_requests }。 */
export interface RefundRequestListResponse {
  refund_requests: RefundRequestView[]
}

/** 退款工单审批/驳回响应:→ { refund_request }。 */
export interface RefundRequestDecisionResponse {
  refund_request: RefundRequestView
}

/** 仪表盘单日序列项,镜像 admin_panel.go:34 dailyStatsView。 */
export interface DailyStats {
  date: string
  order_count: number
  amount_cents: number
}

/** 仪表盘汇总,镜像 admin_panel.go:40 dashboardStatsView。 */
export interface DashboardStats {
  total_amount_cents: number
  total_count: number
  today_count: number
  average_amount_cents: number
  daily_series: DailyStats[]
}
