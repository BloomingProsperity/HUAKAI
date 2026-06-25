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
