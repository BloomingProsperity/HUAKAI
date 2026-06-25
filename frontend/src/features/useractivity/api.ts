import { apiGet } from '../../lib/api'
import type { UserAuditEventsResponse } from './types'

/*
 * 用户安全日志数据访问层。端点 GET /v1/me/audit-events(session 鉴权,tokenForPath 走 session token)。
 * 身份由后端从会话上下文派生(handlers.go:75 SessionFromContext),前端不传任何用户标识,杜绝越权。
 * 真码:backend/internal/userauditloghttp/handlers.go:27、backend/cmd/gateway/routes.go:192。
 */

/** 列出当前用户的审计事件。limit 1-200,offset>=0。 */
export async function listMyAuditEvents(
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<UserAuditEventsResponse> {
  return apiGet<UserAuditEventsResponse>('/v1/me/audit-events', {
    query: { limit, offset },
    signal,
  })
}
