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

/*
 * 自助充值开单 —— 镜像 paymenthttp 的 portalCreateTopupRequest / 创建响应。
 * 端点(真码):POST /v1/users/me/payments/orders(session 鉴权)
 *   backend/internal/paymenthttp/handler.go:209 → user_portal.go:290 newPortalCreateTopupHandler。
 *
 * 请求体真字段(user_portal.go:207 portalCreateTopupRequest):
 *   - amount_cents(int,必需 >0,服务端按门户区间二次裁决)
 *   - provider(string,必需,JSON key 是 "provider" 而非 "provider_kind";须在 config.providers 内)
 *   - terms_version(可选,本前端暂不传)
 * 服务端强制 order_kind=topup、身份取自 session(请求体不带 tenant/user,防越权)。
 *
 * 响应(user_portal.go:357):{order, idempotent, payment_instruction:{provider, instruction}}。
 * money 立场:本响应不含任何 secret;不含余额变更(manual 渠道是 pending 单 + 人工指引,不即时入账)。
 */
export interface CreateTopupRequest {
  amount_cents: number
  provider: string
}

/** 创建充值单响应:复用 orders 模块的 UserOrder 形态(同后端 orderView)。 */
export interface CreateTopupResponse {
  order: import('../orders/types').UserOrder
  idempotent: boolean
  payment_instruction: PortalProviderConfig
}
