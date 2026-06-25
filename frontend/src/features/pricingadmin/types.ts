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
