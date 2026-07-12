/*
 * 我的订单(用户态)前端类型 —— 镜像 paymenthttp 的 user orderView JSON 形态。
 *
 * 端点(真码):
 *   - 列表  GET /v1/users/me/payments/orders        backend/internal/paymenthttp/handler.go:208,310(session)
 *   - 详情  GET /v1/users/me/payments/orders/{id}    backend/internal/paymenthttp/handler.go:210 + user_portal.go:382(session)
 *   - DTO   orderView                                 backend/internal/paymenthttp/handler.go:64
 *
 * 路由前缀 /v1/users/me/payments 挂载于 cmd/gateway/routes.go:298。
 * money 字段一律以后端给的 amount_cents(int)为准,前端纯只读展示,不在此动钱。
 */

/** 用户视角订单 DTO(orderView,snake_case,只含公开字段)。 */
export interface UserOrder {
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
  // 以下为状态机时间戳(omitempty,按订单推进逐个出现);驱动详情页时间线可视化。
  expires_at?: string
  paid_at?: string
  completed_at?: string
}

/** 列表响应:GET /v1/users/me/payments/orders → { orders }(无 count 字段,真码 handler.go:326)。 */
export interface UserOrderListResponse {
  orders: UserOrder[]
}

/** 详情响应:GET /v1/users/me/payments/orders/{id} → { order }(真码 user_portal.go:407)。 */
export interface UserOrderDetailResponse {
  order: UserOrder
}

/*
 * 撤单:POST /v1/users/me/payments/orders/{id}/cancel(真码 handler.go:211 → newUserCancelHandler:218)。
 * 仅 pending 单可撤;非 pending 后端回 409 order_not_cancelable。归属由 session 强制(IDOR 后端兜)。
 * 响应:{ order }(撤单后的订单视图,status 变为 cancelled)。无请求体。
 */
export interface CancelOrderResponse {
  order: UserOrder
}

/*
 * 退款申请:POST /v1/users/me/payments/orders/{id}/refund-request(真码 handler.go:212 → newPortalRefundRequestHandler)。
 * 只对「已完成的充值单」可申请(user_portal.go:451 前置门槛);否则后端回 409 order_not_refund_requestable。
 * money 立场:这只建一条 pending 退款申请记录待 admin 审批,绝不即时动钱。请求体 {reason?} 可选。
 * 响应:{ refund_request }(refundRequestView,不含任何资金事实/secret)。
 */
export interface RefundRequestBody {
  reason?: string
}

/** 退款申请视图(镜像 user_portal.go:226 refundRequestView,仅 pending 意图,不含资金)。 */
export interface RefundRequestView {
  id: number
  order_id: number
  status: string
  reason?: string
  created_at: string
}

export interface RefundRequestResponse {
  refund_request: RefundRequestView
}
