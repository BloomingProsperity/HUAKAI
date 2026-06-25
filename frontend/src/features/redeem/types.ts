/*
 * 兑换码(voucher)前端类型 —— 镜像后端 voucher 包的 JSON 形态。
 * 端点:
 *   POST /v1/users/me/vouchers/redeem  (session 鉴权, BurstLimiter 限流)
 *   GET  /v1/me/voucher-redemptions    (session 鉴权, 兑换历史)
 * 字段名严格对齐 backend/internal/voucher/types.go 与 voucherhttp/redemption_history.go 的 json tag。
 */

/** 兑换请求体。idempotency_key 由前端生成, 防止重复点击/重发造成重复入账。 */
export interface RedeemRequest {
  code: string
  idempotency_key?: string
}

/** 券快照(RedeemResult.voucher, 只取展示需要的字段)。 */
export interface VoucherView {
  id: number
  amount_cents: number
  currency_code: string
  status: string
}

/** 兑换记录(RedeemResult.redemption)。 */
export interface RedemptionRecord {
  voucher_id: number
  amount_cents: number
  currency_code: string
  redeemed_at: string
}

/** 订阅券授予摘要(余额券为空)。 */
export interface SubscriptionGrantView {
  user_subscription_id: number
  plan_id: number
  result_kind: string
  new_expires_at: string
  applied_validity_days: number
}

/**
 * 兑换响应(RedeemResult)。
 * balance_cents = 兑换后账户余额(余额券); idempotent=true 表示命中幂等(已兑过, 未重复入账)。
 * subscription 仅订阅券非空。
 */
export interface RedeemResult {
  voucher: VoucherView
  redemption: RedemptionRecord
  balance_cents: number
  idempotent: boolean
  subscription?: SubscriptionGrantView | null
}

/** 兑换历史项(redemptionView)。 */
export interface RedemptionHistoryItem {
  voucher_id: number
  amount_cents: number
  currency_code: string
  status: string
  redeemed_at: string
  billing_event_id: number
}

export interface RedemptionHistoryResponse {
  redemptions: RedemptionHistoryItem[]
}
