/*
 * 通知偏好(用户自助)前端类型 —— 镜像后端 controlhttp notifySettings JSON 形态。
 * 端点(真码):
 *   - 读 GET /v1/users/me/notifications   backend/internal/controlhttp/notify_handler.go:72(MountNotifyUserRoutes)
 *   - 写 PUT /v1/users/me/notifications   notify_handler.go:73
 * 路由挂载于 backend/cmd/gateway/routes_notifications.go:34(SessionMiddleware 组,session 鉴权)。
 *
 * 安全要点:secret 字段(webhook_secret / gotify_token)后端只回「是否已配置」布尔标志
 * (webhook_secret_configured / gotify_token_configured),绝不回显明文;前端写时才传明文,
 * 不回填到只读视图。balance_threshold 是低余额告警阈值(USD 小数字符串)。
 */

/** GET /v1/users/me/notifications 响应(notifySettingsResponse,notify_handler.go:53)。 */
export interface NotifyPrefsResponse {
  tenant_id: number
  user_id: number
  /** 通知渠道类型:none / email / webhook / bark / gotify 等(后端 notify.Type)。 */
  notify_type: string
  webhook_url?: string
  /** 仅标志位:是否已配置 webhook 密钥(明文绝不回显)。 */
  webhook_secret_configured?: boolean
  notification_email?: string
  bark_url?: string
  gotify_url?: string
  /** 仅标志位:是否已配置 gotify token(明文绝不回显)。 */
  gotify_token_configured?: boolean
  gotify_priority?: number
  /** 低余额告警阈值(USD 小数字符串)。 */
  balance_threshold: string
  /** 额外抄送邮箱(≤10 条,后端校验)。 */
  extra_emails?: string[]
  updated_at?: string
  updated_by?: string
}

/**
 * PUT /v1/users/me/notifications 请求体(notifySettingsRequest,notify_handler.go:38)。
 * 注意:后端用 DisallowUnknownFields,只能带这些字段;secret/token 仅在用户主动填写时才传,
 * 不传则保留现有(read-modify-write 由后端 UpsertSettings 处理)。
 */
export interface NotifyPrefsUpdate {
  notify_type: string
  webhook_url?: string
  webhook_secret?: string
  notification_email?: string
  bark_url?: string
  gotify_url?: string
  gotify_token?: string
  gotify_priority?: number
  balance_threshold?: string
  extra_emails?: string[]
}
