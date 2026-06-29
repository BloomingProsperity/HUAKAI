import { apiGet, ApiError } from '../../lib/api'
import { getTokens } from '../../auth/store'
import { tokenForPath } from '../../auth/tokenForPath'
import type { UsageRecordsResponse } from './types'
import { buildExportQuery } from './usagerecords'

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
