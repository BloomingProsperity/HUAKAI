/*
 * 用户管理(运维台)前端类型 —— 镜像 adminuserhttp 的 JSON。
 * 端点:/admin/v1/users(admin token 鉴权)。
 */
export interface AdminUser {
  id: number
  email: string
  role: string
  status: string
  balance: string
  created_at: string
  // 注:列表端点 userBody 不返回 display_name(routes.go),故列表项不含该字段,避免死读。
}

export interface UserListResponse {
  items: AdminUser[]
  limit: number
  offset: number
}

export interface CreateUserRequest {
  email: string
  password: string
  display_name?: string
  role?: string
}

/*
 * 管理员手动调额(money 敏感)。镜像 adminhttp/balance_credit_handler.go:37 的请求体:
 *   POST /admin/v1/balances/adjustments
 *   amount 的符号即方向 —— 正数=加款(贷记),负数=扣款(借记)。
 *   注:后端目前仅放行加款,扣款(负数 amount)会被 ErrAdminDebitNotSupported 拒(400
 *   admin_debit_not_yet_supported,balance_credit_handler.go:119 / admin_credit.go:104)。
 *   tenant_id 为目标租户(用户详情体不含,故运维台需显式提供);currency_code 省略默认 USD;
 *   idempotency_key 用于把重复提交合并为一次入账(前端为同一意图复用同一 key)。
 */
export interface BalanceAdjustmentRequest {
  tenant_id: number
  user_id: number
  amount: string
  currency_code?: string
  reason: string
  idempotency_key: string
}

/*
 * 调额响应(balance_credit_handler.go:46)。net_balance 为入账后净余额(StringFixed(8)),
 * recharge_order_id 仅加款生成充值单时回传。
 */
export interface BalanceAdjustmentResult {
  tenant_id: number
  user_id: number
  net_balance: string
  currency_code: string
  recharge_order_id?: number
}

/*
 * 通知偏好(管理员代管)前端类型 —— 镜像 controlhttp notifyAdminHandler 的 JSON 形态,
 * 与用户自助 /v1/users/me/notifications 同构(同一 notifySettingsRequest/Response)。
 * 端点(真码):
 *   - 读 GET /v1/admin/users/{user_id}/notifications  notify_handler.go:78(MountNotifyAdminRoutes)
 *   - 写 PUT /v1/admin/users/{user_id}/notifications  notify_handler.go:79
 * 挂载于 routes_notifications.go:37(NotifyAdminDeps,admin token 鉴权)。
 * 目标租户:notifyAdminTarget(notify_handler.go:194)用 ?tenant_id= 定位;platform_admin 必须
 * 显式给(否则 400 tenant_id_required),tenant_operator 省略则回落到自身 scope。
 *
 * 安全要点:secret 字段(webhook_secret / gotify_token)后端只回「是否已配置」布尔标志
 * (webhook_secret_configured / gotify_token_configured,notify_handler.go:262/266),绝不回显明文。
 */

/** GET /v1/admin/users/{user_id}/notifications 响应(notifySettingsResponse,notify_handler.go:53)。 */
export interface AdminNotifyResponse {
  tenant_id: number
  user_id: number
  /** 通知渠道类型:none / email / webhook / bark / gotify(后端 notify.Type)。 */
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
 * PUT /v1/admin/users/{user_id}/notifications 请求体(notifySettingsRequest,notify_handler.go:38)。
 * 后端用 DisallowUnknownFields,只能带这些字段;secret/token 仅在管理员本次输入明文时才下传。
 * ⚠️ 后端 UpsertSettings 对 webhook_secret/gotify_token 无条件覆盖(store.go:209/213,EXCLUDED.*),
 * 故「留空」语义=清除已存密钥(非保留),清除前由卡片二次确认。
 */
export interface AdminNotifyUpdate {
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
