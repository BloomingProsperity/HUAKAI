import { apiGet, apiSend } from '../../lib/api'
import type { NotificationListResponse, UnreadCountResponse } from './types'

/*
 * 站内信数据访问层。端点均为 session 鉴权(tokenForPath 对 /v1/notifications 发 session token)。
 * 后端:internal/usernoticehttp/handlers.go(MountUserRoutes,routes_notifications.go 在
 * SessionMiddleware 组内挂载)。
 */

/** 列表。limit 1-100、offset≥0、unread_only 只看未读;均为后端校验的查询参数。 */
export function listNotifications(
  opts: { limit?: number; offset?: number; unreadOnly?: boolean } = {},
  signal?: AbortSignal,
): Promise<NotificationListResponse> {
  return apiGet<NotificationListResponse>('/v1/notifications', {
    query: {
      limit: opts.limit,
      offset: opts.offset,
      // 只在需要时带 unread_only=true;不需要时省略(后端默认 false)。
      unread_only: opts.unreadOnly ? 'true' : undefined,
    },
    signal,
  })
}

/** 未读数(铃铛角标用)。 */
export function getUnreadCount(signal?: AbortSignal): Promise<UnreadCountResponse> {
  return apiGet<UnreadCountResponse>('/v1/notifications/unread-count', { signal })
}

/** 标记单条已读。后端幂等(已读再标无副作用)。 */
export function markNotificationRead(id: number): Promise<unknown> {
  return apiSend<unknown>('POST', `/v1/notifications/${id}/read`, {})
}
