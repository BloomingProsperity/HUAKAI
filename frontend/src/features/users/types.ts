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
