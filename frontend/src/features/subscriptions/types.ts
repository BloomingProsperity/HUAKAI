/*
 * 订阅页(user 壳)前端类型 —— 镜像后端 subscriptionhttp 的 JSON 形态。
 * 端点挂载在 /v1/users/me/subscriptions(session 鉴权,由 lib/api 按路径自动注入 session token)。
 *   - planView                 → backend/internal/subscriptionhttp/handler.go:128
 *   - subscriptionView         → handler.go:147
 *   - currentSubscriptionView  → purchase.go:55
 *   - subscriptionProgressView → purchase.go:65
 *   - purchaseOrderView        → purchase.go:44
 * 金额上限字段(daily/weekly/monthly_cap_usd)后端是 *string(numeric 字符串),
 * 进度的 cap/consumed/remaining/overage 也是 USD 小数字符串;前端不做浮点重算,只展示后端给的串。
 */

/** 在售套餐(GET /plans 的元素;后端只回 for_sale && enabled 的套餐)。 */
export interface PlanView {
  id: number
  tenant_id: number
  name: string
  description?: string
  /** 价格,单位「分」。 */
  price_cents: number
  currency_code: string
  validity_days: number
  granted_group?: string
  /** USD 小数字符串;缺省表示该窗口不设上限。 */
  daily_cap_usd?: string | null
  weekly_cap_usd?: string | null
  monthly_cap_usd?: string | null
  for_sale: boolean
  enabled: boolean
  sort_order: number
  created_at: string
  updated_at: string
}

export interface ListPlansResponse {
  plans: PlanView[]
}

/** 当前生效订阅(GET /me 与 /me/progress 的 subscription 字段)。 */
export interface SubscriptionView {
  id: number
  plan_id: number
  granted_group?: string
  daily_cap_usd?: string | null
  weekly_cap_usd?: string | null
  monthly_cap_usd?: string | null
  status: string
  starts_at: string
  expires_at: string
  cancelled_at?: string | null
  created_at: string
}

/** GET /me:当前订阅 + 是否开启自动续订。subscription 为 null 表示当前无生效订阅。 */
export interface CurrentSubscriptionResponse {
  subscription: SubscriptionView | null
  auto_renew: boolean
}

/** GET /me/progress 的单个配额窗口进度项。 */
export interface SubscriptionProgressView {
  /** 窗口类型:calendar_day / calendar_week / calendar_month。 */
  window_kind: string
  /** 以下四项均为 USD 小数字符串。 */
  cap: string
  consumed: string
  remaining: string
  overage: string
  request_count: number
  window_start: string
  window_end: string
  /** consumed/cap*100;cap 为 0 时后端返回 0(不会越界除零);可超过 100(超额时)。 */
  usage_percent: number
  /** 距本窗口重置的秒数;窗口已过返回 0。 */
  resets_in_seconds: number
  over_limit: boolean
  over_limit_amount: string
}

export interface SubscriptionProgressResponse {
  subscription: SubscriptionView | null
  progress: SubscriptionProgressView[]
}

/** POST /purchase 请求体。 */
export interface PurchaseRequest {
  plan_id: number
}

/** 购买订单摘要(POST /purchase 响应的 order 字段)。 */
export interface PurchaseOrderView {
  id: number
  out_trade_no: string
  status: string
  amount_cents: number
  currency_code: string
  order_kind: string
  subscription_plan_id?: number | null
}

/** POST /purchase 响应:建单成功返回订单 + 支付指引;idempotent 表示重复下单命中既有订单。 */
export interface PurchaseResponse {
  order: PurchaseOrderView
  idempotent: boolean
  payment_instruction: string
}
