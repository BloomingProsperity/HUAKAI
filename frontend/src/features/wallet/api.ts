import { apiGet } from '../../lib/api'
import type { BalanceResponse, PortalConfigResponse } from './types'

/*
 * 钱包数据访问层。余额走 GET /v1/users/me/payments/balance(session,真码 handler.go:213);
 * 充值配置走 GET /v1/users/me/payments/config(session,真码 user_portal.go:265 newPortalConfigHandler);
 * 最近订单复用我的订单模块 listMyOrders。纯只读,不在此动钱。
 * 两个端点均挂在 /v1/users/me 前缀下,lib/api 自动走 session 鉴权(非 admin Bearer)。
 */
const BALANCE_PATH = '/v1/users/me/payments/balance'
const CONFIG_PATH = '/v1/users/me/payments/config'

export async function getMyBalance(signal?: AbortSignal): Promise<BalanceResponse> {
  return apiGet<BalanceResponse>(BALANCE_PATH, { signal })
}

/** 取门户可充配置(金额区间 + 预设金额 + 启用渠道指引)。纯只读,不开单。 */
export async function getPortalConfig(signal?: AbortSignal): Promise<PortalConfigResponse> {
  return apiGet<PortalConfigResponse>(CONFIG_PATH, { signal })
}
