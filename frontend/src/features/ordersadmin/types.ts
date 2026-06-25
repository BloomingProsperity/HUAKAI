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
