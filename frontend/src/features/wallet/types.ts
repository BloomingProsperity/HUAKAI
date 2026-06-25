/*
 * 钱包(用户态)前端类型 —— 镜像 paymenthttp 的 balanceView。
 * 端点(真码):GET /v1/users/me/payments/balance(session 鉴权)→ {balance:{...}}
 *   backend/internal/paymenthttp/handler.go:213 newUserBalanceHandler。
 * 充值=手动(联系管理员)/自助开单走 POST /orders;本页只读展示余额 + 最近订单。
 */
export interface UserBalance {
  tenant_id: number
  user_id: number
  amount_cents: number
}

export interface BalanceResponse {
  balance: UserBalance
}
