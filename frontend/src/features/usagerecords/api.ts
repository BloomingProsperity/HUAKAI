import { apiGet } from '../../lib/api'
import type { UsageRecordsResponse } from './types'

/*
 * 用量明细数据访问层。端点 GET /v1/me/usage-records(session 鉴权,tokenForPath 走 session token)。
 * 身份由后端从会话上下文派生(session_handler.go),前端不传任何用户标识,杜绝越权。
 * 真码:backend/internal/meusagehttp/session_handler.go:19、backend/cmd/gateway/routes.go:192。
 * 游标分页:首页不传 cursor;翻页传上一页返回的 next_cursor。
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
