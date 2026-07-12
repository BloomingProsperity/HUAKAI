import { apiGet, apiSend } from '../../lib/api'
import type { BalanceResponse, CreateTopupRequest, CreateTopupResponse, PortalConfigResponse } from './types'

/*
 * 钱包数据访问层。余额走 GET /v1/users/me/payments/balance(session,真码 handler.go:213);
 * 充值配置走 GET /v1/users/me/payments/config(session,真码 user_portal.go:265 newPortalConfigHandler);
 * 自助开单走 POST /v1/users/me/payments/orders(session,真码 handler.go:209 → user_portal.go:290);
 * 最近订单复用我的订单模块 listMyOrders。
 * 所有端点均挂在 /v1/users/me 前缀下,lib/api 自动走 session 鉴权(非 admin Bearer)。
 */
const BALANCE_PATH = '/v1/users/me/payments/balance'
const CONFIG_PATH = '/v1/users/me/payments/config'
const ORDERS_PATH = '/v1/users/me/payments/orders'

export async function getMyBalance(signal?: AbortSignal): Promise<BalanceResponse> {
  return apiGet<BalanceResponse>(BALANCE_PATH, { signal })
}

/** 取门户可充配置(金额区间 + 预设金额 + 启用渠道指引)。纯只读,不开单。 */
export async function getPortalConfig(signal?: AbortSignal): Promise<PortalConfigResponse> {
  return apiGet<PortalConfigResponse>(CONFIG_PATH, { signal })
}

/**
 * 自助创建充值订单(建 pending 单 + 返回人工支付指引,不即时入账)。
 * money 敏感:UI 调用前已二次确认金额。请求体只带 amount_cents + provider;
 * 身份/order_kind 由服务端从 session 强制(handler.go:209 → user_portal.go:290),前端绝不传 tenant/user。
 * 响应不含任何 secret(后端 orderView 仅公开字段)。
 */
export async function createTopupOrder(body: CreateTopupRequest): Promise<CreateTopupResponse> {
  return apiSend<CreateTopupResponse>('POST', ORDERS_PATH, body)
}
