/*
 * 新建账号向导用到的目录类型 —— 镜像后端 DTO:
 *  - 模式目录   GET /admin/v1/account-modes      → adminhttp.Catalog
 *  - 上游目录   GET /admin/v1/providers          → providerCatalogListResponse
 *  - 渠道目录   GET /admin/v1/channels           → channelCatalogListResponse
 *  - 创建账号   POST /admin/v1/provider-accounts → createProviderAccountRequest
 */

/** 凭据字段渲染规格(credentialacq.FieldSpec)。 */
export interface FieldSpec {
  name: string
  /** secret | string | url | select | textarea | json_object | boolean */
  kind: string
  required: boolean
  one_of_group?: string
  input?: string
  /** secret | none —— secret 走密文输入,不回显。 */
  redaction: string
  /** credential | oauth_client | cloud | advanced */
  group: string
}

/** 账号凭据模式(adminhttp.Mode)。 */
export interface AccountMode {
  vendor: string
  auth_mode: string
  flow_kind: string
  client_identity_source: string
  manual_first: boolean
  long_lived_toggle: boolean
  allowed_helpers: string[]
  required_fields: FieldSpec[]
  is_enabled: boolean
  is_experimental: boolean
  feature_flag: string
  /** low | medium | high 等风险等级 */
  risk_level: string
  risk_reasons: string[]
}

export interface AccountModesResponse {
  modes: AccountMode[]
}

export interface ProviderCatalogItem {
  id: number
  code: string
  display_name: string
  upstream_protocol: string
  enabled: boolean
}

export interface ProviderCatalogResponse {
  object: string
  items: ProviderCatalogItem[]
}

export interface ChannelCatalogItem {
  id: number
  pool_group_id: number
  name: string
  enabled: boolean
}

export interface ChannelCatalogResponse {
  object: string
  items: ChannelCatalogItem[]
}

/** 创建账号请求体(createProviderAccountRequest 的子集,只发向导覆盖的字段)。 */
export interface CreateAccountRequest {
  provider_id: number
  channel_id: number
  name: string
  account_type: string
  vendor?: string
  auth_mode?: string
  confirm?: boolean
  enabled?: boolean
  priority?: number
  static_weight?: number
  cap_concurrency?: number
  probe_model?: string
  tags?: string[]
  credentials: Record<string, string>
  reason?: string
}

/** 混合渠道风险确认结果:后端以 HTTP 400 + error:"mixed_channel_risk_confirmation_required" 返回。 */
export interface MixedRiskRequired {
  mixedRisk: true
  risks: unknown[]
  message: string
}
