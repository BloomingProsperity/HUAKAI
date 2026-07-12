/*
 * 模型定价设置(运维台)DTO。字段名严格对齐后端 JSON tag,核源码为准:
 *  - 分组倍率   internal/pricingcataloghttp/pricing_ratio_handler.go(ratioResponseBody / ratioListResponseBody)
 *  - 缓存价覆盖 internal/paymenthttp/cache_price_override_handler.go(cacheOverrideView)
 *  - 工具附加费 internal/toolpricing/toolpricing.go(DefaultToolPrices,无 admin 端点 → 只读展示)
 */

/** 单条分组倍率。后端仅当 public_ratio=true 时回传 ratio 字段(否则省略,表示对外隐藏倍率)。 */
export interface PricingRatio {
  object: string
  id: number
  tenant_id: number
  pool_group_id: number
  /** 正小数字符串;public_ratio=false 时后端不回传(omitempty)。 */
  ratio?: string
  public_ratio: boolean
  created_by?: string
  updated_by?: string
  created_at?: string
  updated_at?: string
}

export interface PricingRatioListResponse {
  object: string
  items: PricingRatio[]
  limit: number
  offset: number
}

/** PUT /{pool_group_id} 请求体。 */
export interface UpsertRatioRequest {
  ratio: string
  public_ratio: boolean
}

/** 审计链完整性证明结果(GET /audit/verify)。 */
export interface RatioAuditVerifyResponse {
  object: string
  ok: boolean
  row_id?: number
  reason?: string
}

/** 缓存价覆盖 scope:global=全局,model=按模型,tenant=按租户。 */
export type CacheOverrideScope = 'global' | 'model' | 'tenant'

/** 单条缓存价覆盖。未列出的 scope 表示走官方价。 */
export interface CacheOverride {
  scope: string
  /** model scope 时为模型名。 */
  model?: string
  /** tenant scope 时为租户 id。 */
  tenant_id?: number
  /** 正小数字符串(倍率)。 */
  multiplier: string
  updated_at: string
}

export interface CacheOverrideListResponse {
  overrides: CacheOverride[]
}

/** PUT /{scope} 请求体。 */
export interface SetCacheOverrideRequest {
  multiplier: string
}

/* ---- 计费策略(流式仅输入后中断的结算策略)---- */
/* 后端真码:internal/gatewayhttp/admin_billing_settings_handler.go(adminBillingSettingsResponse / adminBillingSettingsPutRequest)
 * 端点 GET/PUT /admin/v1/billing/settings;策略键固定 stream_input_only_interrupted_policy。 */

/** GET/PUT /admin/v1/billing/settings 响应。 */
export interface BillingSettingsResponse {
  tenant_id: number
  /** 策略键(固定 stream_input_only_interrupted_policy)。 */
  key: string
  /** 当前生效值(no_bill / no_bill_record)。 */
  value: string
  /** 取值来源:default=用全局默认;tenant=该租户已自定义。 */
  source: string
  /** 可选值(no_bill / no_bill_record)。 */
  allowed_values: string[]
  /** 路线图值(bill_input,当前阶段不可启用)。 */
  roadmap_values: string[]
  updated_at?: string | null
  updated_by?: string | null
}

/** PUT /admin/v1/billing/settings 请求体。reason 必填(写入审计)。 */
export interface UpdateBillingSettingsRequest {
  tenant_id: number
  /** 目标策略值(必须是 allowed_values 之一)。 */
  stream_input_only_interrupted_policy: string
  reason: string
}

/** 工具附加费默认价(USD / 1000 次调用)。来自后端 toolpricing.DefaultToolPrices,无写端点。 */
export interface ToolSurchargeDefault {
  /** 工具标识(英文,后端标识符不翻译)。 */
  tool: string
  /** 中文展示名。 */
  label: string
  /** 每 1000 次调用的美元价。 */
  perThousandUSD: string
  /** 备注(如来源 / 是否延期)。 */
  note: string
}
