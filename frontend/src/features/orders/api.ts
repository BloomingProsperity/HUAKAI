import { apiGet, ApiError } from '../../lib/api'
import { getTokens } from '../../auth/store'
import { tokenForPath } from '../../auth/tokenForPath'
import type { UserOrderDetailResponse, UserOrderListResponse } from './types'

/*
 * 我的订单数据访问层。端点前缀 /v1/users/me/payments(session 鉴权,管理当前登录用户名下的订单)。
 * 纯只读:本模块只 GET,不发起任何写/退款资金动作(退款申请是另一条非只读路径,本页不做)。
 *
 * 端点真码:
 *   - 列表 backend/internal/paymenthttp/handler.go:208 → newUserListOrdersHandler(handler.go:310)
 *   - 详情 backend/internal/paymenthttp/handler.go:210 → newPortalGetOrderHandler(user_portal.go:382)
 *   - 收据 backend/internal/invoicehttp/handler.go:33(GET /v1/me/orders/{id}/receipt,挂载 routes.go:197 的 /v1/me session 组)
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

/**
 * 下载订单收据。后端 invoicehttp 返回的是 text/plain 文本(非 JSON、非附件头),
 * 故不能走 apiGet(它按 JSON 解析)。这里用带 session Authorization 头的同源 fetch 取文本,
 * 失败时把后端 JSON 错误体({error:{code,message}})归一化成 ApiError 交上层提示。
 * 收据仅对「已完成的充值/订阅订单」可得(receiptEligible 已先行过滤),否则后端回 404/409。
 */
export async function fetchOrderReceipt(id: number, signal?: AbortSignal): Promise<string> {
  const path = `/v1/me/orders/${id}/receipt`
  // /v1/me/* 走 session token(tokenForPath:非 /v1/auth、非 admin → session)。
  const token = tokenForPath(path, getTokens())
  const resp = await fetch(path, {
    method: 'GET',
    credentials: 'include',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    signal,
  })
  const text = await resp.text()
  if (!resp.ok) {
    let code = `http_${resp.status}`
    let message = resp.statusText || '获取收据失败'
    try {
      const body = text ? JSON.parse(text) : undefined
      if (body?.error) {
        code = body.error.code ?? code
        message = body.error.message ?? message
      }
    } catch {
      /* 非 JSON 错误体,沿用状态文案 */
    }
    throw new ApiError(resp.status, code, message)
  }
  return text
}

/**
 * 触发浏览器把收据文本另存为 .txt 文件。用 Blob + 临时 a[download],下载后释放 objectURL。
 * 抽成独立函数便于复用,DOM 副作用集中在此一处。
 */
export function downloadReceiptText(orderId: number, outTradeNo: string, content: string): void {
  const blob = new Blob([content], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  // 文件名优先用商户单号,缺失回落订单 id。
  a.download = `receipt-${outTradeNo || orderId}.txt`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}
