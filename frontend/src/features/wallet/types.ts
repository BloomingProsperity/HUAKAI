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

/*
 * 门户充值配置(只读)——镜像 paymenthttp 的 portalConfigView / portalProviderConfigView。
 * 端点(真码):GET /v1/users/me/payments/config(session 鉴权)→ {config:{...}}
 *   backend/internal/paymenthttp/handler.go:214 → user_portal.go:265 newPortalConfigHandler。
 * 仅展示安全运行时配置(金额区间 + 预设金额 + 启用渠道的人工支付指引),不接自助开单(Owner-gated 真支付)。
 */

/** 单个支付渠道配置:provider 已小写归一(如 "manual"/"taobao"),instruction 为人工支付指引文案。 */
export interface PortalProviderConfig {
  provider: string
  instruction: string
}

/** 门户可充配置:金额区间(分)+ 预设金额数组(分)+ 币种 + 启用渠道列表。 */
export interface PortalTopupConfig {
  min_topup_cents: number
  max_topup_cents: number
  preset_amount_cents: number[]
  currency_code: string
  providers: PortalProviderConfig[]
}

export interface PortalConfigResponse {
  config: PortalTopupConfig
}
