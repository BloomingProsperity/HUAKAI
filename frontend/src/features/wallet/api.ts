import { apiGet } from '../../lib/api'
import type { BalanceResponse } from './types'

/*
 * 钱包数据访问层。余额走 GET /v1/users/me/payments/balance(session,真码 handler.go:213);
 * 最近订单复用我的订单模块 listMyOrders。纯只读,不在此动钱。
 */
const BALANCE_PATH = '/v1/users/me/payments/balance'

export async function getMyBalance(signal?: AbortSignal): Promise<BalanceResponse> {
  return apiGet<BalanceResponse>(BALANCE_PATH, { signal })
}
