import { apiGet, apiSend, ApiError, ensureFreshSessionForPath } from '../../lib/api'
import { getTokens } from '../../auth/store'
import { tokenForPath } from '../../auth/tokenForPath'
import type {
  CreateDisputeResponse,
  DisputesResponse,
  ReceiptVerifyResponse,
  UsageRecordsResponse,
  UserCostReceipt,
} from './types'
import { buildExportQuery, encodeReceiptRequestID } from './usagerecords'

/*
 * 用量明细数据访问层。端点 GET /v1/me/usage-records(session 鉴权,tokenForPath 走 session token)。
 * 身份由后端从会话上下文派生(session_handler.go),前端不传任何用户标识,杜绝越权。
 * 真码:backend/internal/meusagehttp/session_handler.go:19、backend/cmd/gateway/routes.go:193。
 * 游标分页:首页不传 cursor;翻页传上一页返回的 next_cursor。
 * CSV 导出:GET /v1/me/usage/export.csv(meexporthttp/handler.go:59,挂载 routes.go:194 的 /v1/me session 组)。
 */

export interface ListUsageRecordsQuery {
  /** 每页条数,1-200(后端 parseQuery 校验),默认 50。 */
  limit?: number
  /** 不透明游标(上一页的 next_cursor);省略=首页。 */
  cursor?: string
  /** 起止时间(RFC3339);可选。 */
  from?: string
  to?: string
}

export async function listUsageRecords(
  q: ListUsageRecordsQuery = {},
  signal?: AbortSignal,
): Promise<UsageRecordsResponse> {
  return apiGet<UsageRecordsResponse>('/v1/me/usage-records', {
    query: { limit: q.limit ?? 50, cursor: q.cursor, from: q.from, to: q.to },
    signal,
  })
}

/**
 * 导出用量为 CSV 并触发浏览器下载。
 * 后端返回 text/csv(带 Content-Disposition attachment),不是 JSON,故不能走 apiGet;
 * 这里用带 session Authorization 头的同源 fetch 取 blob,再用临时 a[download] 另存。
 * from/to 为 'YYYY-MM-DD',经 buildExportQuery 转 RFC3339(右界半开覆盖整日)。
 * 失败时把后端 JSON 错误体归一化成 ApiError 交上层提示。
 */
export async function exportUsageCSV(fromDay: string, toDay: string, signal?: AbortSignal): Promise<void> {
  const path = '/v1/me/usage/export.csv'
  const q = buildExportQuery(fromDay, toDay)
  const url = `${path}?format=${encodeURIComponent(q.format)}&from=${encodeURIComponent(q.from)}&to=${encodeURIComponent(q.to)}`
  await ensureFreshSessionForPath(path)
  // /v1/me/* 走 session token(tokenForPath:非 /v1/auth、非 admin → session)。
  const token = tokenForPath(path, getTokens())
  const resp = await fetch(url, {
    method: 'GET',
    credentials: 'include',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    signal,
  })
  if (!resp.ok) {
    // 错误体为 JSON({error:{code,message}});读出归一化成 ApiError。
    const text = await resp.text().catch(() => '')
    let code = `http_${resp.status}`
    let message = resp.statusText || '导出失败'
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
  const blob = await resp.blob()
  const objectUrl = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = objectUrl
  a.download = `usage-${fromDay}_${toDay}.csv`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(objectUrl)
}

/*
 * ── 签名收据 / 验签 / 我的争议(session 只读数据层) ───────────────────────────
 * 这几条都 session 鉴权(tokenForPath:非 /v1/auth、非 admin 前缀 → session token;
 * 后端 routes.go:174-184 均挂 SessionMiddleware)。身份由后端从会话派生,前端只传 request_id。
 * 注:逐请求成本端点 /v1/generation 走的是 API-key(d.inboundAuth,routes.go:130 顶层)鉴权、
 * 非 session 不可达,故前端不接它;成本/用量明细改用列表行(/v1/me/usage-records,session)自带数据。
 */

/**
 * 单次签名成本收据:GET /v1/receipts/{request_id}(cost_receipt_handler.go:101)。
 * request_id 可能含至多一个斜杠(host/tail 形态),encodeReceiptRequestID 按段编码以匹配后端路由。
 * 收据尚未最终化时后端回 202 receipt_unavailable;此处统一交上层按 ApiError 处理。
 */
export async function getCostReceipt(requestID: string, signal?: AbortSignal): Promise<UserCostReceipt> {
  return apiGet<UserCostReceipt>(`/v1/receipts/${encodeReceiptRequestID(requestID)}`, { signal })
}

/**
 * 收据验签:POST /v1/receipts/{request_id}/verify(cost_receipt_handler.go:148)。
 * 只读密码学校验,不动钱。空 body 即「验存储的签名收据」分支(verifyStoredCostReceiptByID),
 * 后端用已落库的 canonical+签名做校验,前端无需也不应自带 receipt 体。
 */
export async function verifyCostReceipt(requestID: string, signal?: AbortSignal): Promise<ReceiptVerifyResponse> {
  return apiSend<ReceiptVerifyResponse>('POST', `/v1/receipts/${encodeReceiptRequestID(requestID)}/verify`, undefined, {
    signal,
  })
}

/**
 * 我的争议列表:GET /v1/me/disputes(dispute_handler.go:116)。只读列本人争议。
 * limit 1-500(后端 parseLimit 校验),默认 100。
 */
export async function listMyDisputes(limit?: number, signal?: AbortSignal): Promise<DisputesResponse> {
  return apiGet<DisputesResponse>('/v1/me/disputes', {
    query: { limit: limit ?? undefined },
    signal,
  })
}

/**
 * 对某条成本收据发起争议:POST /v1/receipts/{request_id}/disputes
 * (dispute_handler.go:75 NewCreateDisputeHandler,挂 routes.go:177/181,session 鉴权)。
 * 语义(write-only · 不立即动钱):仅创建一条 pending 争议记录;裁决/退款由 admin 侧
 * /v1/admin/disputes/{id}/resolve 人工处理(NewAdminResolveDisputeHandler,dispute_handler.go:172),
 * 本端点绝不动余额。身份由后端从会话上下文派生(ident.TenantID/UserID),前端不传用户标识。
 * request_id 经 encodeReceiptRequestID 适配单段 {request_id} / 双段 {host}/{tail} 两套路由。
 * 后端先校验该收据归属当前用户(GetReceiptForUser),不存在→404 receipt_not_found;
 * reason 必填且去空白后 ≤4000(dispute_store.go:197-200,否则 400 invalid_dispute_request);
 * 同一收据重复发起→409 dispute_duplicate。成功回 201 + {dispute}。
 */
export async function createDispute(
  requestID: string,
  reason: string,
  signal?: AbortSignal,
): Promise<CreateDisputeResponse> {
  return apiSend<CreateDisputeResponse>(
    'POST',
    `/v1/receipts/${encodeReceiptRequestID(requestID)}/disputes`,
    { reason },
    { signal },
  )
}
