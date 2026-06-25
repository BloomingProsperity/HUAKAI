import { apiGet } from '../../lib/api'
import type { UserOrderDetailResponse, UserOrderListResponse } from './types'

/*
 * 我的订单数据访问层。端点前缀 /v1/users/me/payments(session 鉴权,管理当前登录用户名下的订单)。
 * 纯只读:本模块只 GET,不发起任何写/退款资金动作(退款申请是另一条非只读路径,本页不做)。
 *
 * 端点真码:
 *   - 列表 backend/internal/paymenthttp/handler.go:208 → newUserListOrdersHandler(handler.go:310)
 *   - 详情 backend/internal/paymenthttp/handler.go:210 → newPortalGetOrderHandler(user_portal.go:382)
 */
const ORDERS_PATH = '/v1/users/me/payments/orders'

/**
 * 拉取我的订单列表。后端仅接受 limit(1-200,越界回落 50;真码 handler.go:390 parseLimit),
 * 不支持 status/offset 查询参数 —— 状态筛选与翻页在前端对该窗口内做。
 */
export async function listMyOrders(limit = 50, signal?: AbortSignal): Promise<UserOrderListResponse> {
  return apiGet<UserOrderListResponse>(ORDERS_PATH, { query: { limit }, signal })
}

/** 拉取单张订单详情(归属校验在后端;非本人订单回 404,真码 user_portal.go:403)。 */
export async function getMyOrder(id: number, signal?: AbortSignal): Promise<UserOrderDetailResponse> {
  return apiGet<UserOrderDetailResponse>(`${ORDERS_PATH}/${id}`, { signal })
}
