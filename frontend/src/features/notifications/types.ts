/*
 * 站内信(用户通知收件箱)类型。对应后端 usernoticehttp 用户面端点:
 * GET /v1/notifications(列表,{object,items,limit,offset})、
 * GET /v1/notifications/unread-count({object,count})、
 * POST /v1/notifications/{id}/read(标记已读)。均 session 鉴权。
 */

/** 单条通知(后端 notificationResponse:read_at 为空=未读)。 */
export interface UserNotification {
  id: number
  tenant_id: number
  user_id: number
  title: string
  body: string
  /** info | warning | critical(后端默认 info)。 */
  severity: string
  /** 已读时间;为空/缺失表示未读。 */
  read_at?: string | null
  created_by_admin?: number | null
  created_at: string
}

/** GET /v1/notifications 响应(分页)。 */
export interface NotificationListResponse {
  object: string
  items: UserNotification[]
  limit: number
  offset: number
}

/** GET /v1/notifications/unread-count 响应。 */
export interface UnreadCountResponse {
  object: string
  count: number
}
