import { apiGet, apiSend } from '../../lib/api'
import type { RedeemRequest, RedeemResult, RedemptionHistoryResponse } from './types'

/*
 * 兑换码数据访问层。两个端点均 session 鉴权(由 lib/api 按路径自动注入 session token)。
 *   - 兑换:POST /v1/users/me/vouchers/redeem
 *   - 历史:GET  /v1/me/voucher-redemptions?limit=N
 */
const REDEEM_PATH = '/v1/users/me/vouchers/redeem'
const HISTORY_PATH = '/v1/me/voucher-redemptions'

/** 兑换一张券。req 由纯逻辑 buildRedeemRequest 构造(已附 idempotency_key)。 */
export async function redeemVoucher(req: RedeemRequest, signal?: AbortSignal): Promise<RedeemResult> {
  return apiSend<RedeemResult>('POST', REDEEM_PATH, req, { signal })
}

/** 拉取当前用户的兑换历史(后端按用户隔离, limit 1..200)。 */
export async function listRedemptions(limit = 50, signal?: AbortSignal): Promise<RedemptionHistoryResponse> {
  return apiGet<RedemptionHistoryResponse>(HISTORY_PATH, { query: { limit }, signal })
}
