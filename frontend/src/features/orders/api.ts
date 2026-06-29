import { apiGet, apiSend, ApiError } from '../../lib/api'
import { getTokens } from '../../auth/store'
import { tokenForPath } from '../../auth/tokenForPath'
import type {
  CancelOrderResponse,
  RefundRequestBody,
  RefundRequestResponse,
  UserOrderDetailResponse,
  UserOrderListResponse,
} from './types'

/*
 * 我的订单数据访问层。端点前缀 /v1/users/me/payments(session 鉴权,管理当前登录用户名下的订单)。
 * 读:列表 / 详情 / 收据。写:撤自己的 pending 单 + 对已完成单发起退款申请(只建 pending 记录,不动钱)。
 *
 * 端点真码:
 *   - 列表 backend/internal/paymenthttp/handler.go:208 → newUserListOrdersHandler(handler.go:310)
 *   - 详情 backend/internal/paymenthttp/handler.go:210 → newPortalGetOrderHandler(user_portal.go:382)
 *   - 撤单 backend/internal/paymenthttp/handler.go:211 → newUserCancelHandler(handler.go:218)
 *   - 退款申请 backend/internal/paymenthttp/handler.go:212 → newPortalRefundRequestHandler(user_portal.go:414)
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
 * 撤销自己的 pending 充值单(扫码/淘宝下单前可撤)。无请求体。
 * 仅 pending 可撤(orderEditable.cancellable 已先行过滤);非 pending 后端回 409 order_not_cancelable。
 * 身份取自 session,非本人订单后端回 404(IDOR 后端兜)。撤单不动钱(pending 单从未入账)。
 */
export async function cancelMyOrder(id: number): Promise<CancelOrderResponse> {
  return apiSend<CancelOrderResponse>('POST', `${ORDERS_PATH}/${id}/cancel`, undefined)
}

/**
 * 对自己「已完成的充值单」发起退款申请 → 只建一条 pending 记录待 admin 审批,绝不即时动钱。
 * 仅 completed+topup 可申请(orderEditable.refundRequestable 已先行过滤);否则后端回 409。
 * 请求体 {reason?} 可选;reason 空时不带 body。
 */
export async function requestOrderRefund(id: number, body?: RefundRequestBody): Promise<RefundRequestResponse> {
  const reason = body?.reason?.trim()
  return apiSend<RefundRequestResponse>('POST', `${ORDERS_PATH}/${id}/refund-request`, reason ? { reason } : undefined)
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
