/*
 * 管理员计费运营观测(只读)前端类型 —— 镜像 admin_observability_handler 的 JSON 形态。
 * 两个端点(均 admin token 鉴权、纯只读、游标分页):
 *   - GET /admin/v1/usage          原始逐笔用量成本表(mapUsageRow 映射)
 *   - GET /admin/v1/billing/claims 预扣/结算 claim 台账(原始 row 直出)
 * 真码:backend/internal/gatewayhttp/admin_observability_handler.go:66/70 +
 *      backend/internal/gatewayhttp/admin_observability_helpers.go:23(mapUsageRow)+
 *      backend/internal/db/billing/observability.sql.go:505/656(row struct)。
 *
 * money 字段(actual_cost / predicted_cost)后端用 decimal/NullDecimal 序列化为【十进制字符串】,
 * 这里一律用 string|null 接收并【原样渲染】,绝不转 number,避免精度丢失。
 */

/** 通用观测列表响应封套(items / next_cursor / total)。obsListResponse(handler.go:60)。 */
export interface ObsListResponse<T> {
  items: T[]
  /** 下一页游标(opaque base64);空串表示已到末页。 */
  next_cursor: string
  /** 满足过滤条件的总条数(不受游标分页影响)。 */
  total: number
}

/*
 * 原始逐笔用量记录。字段镜像 mapUsageRow(admin_observability_helpers.go:42-59)。
 * 时间戳为 RFC3339Nano 字符串或 null(pgtype.Timestamptz.MarshalJSON)。
 * actual_cost 为十进制字符串(decimal.Decimal 永不为 null)。
 */
export interface UsageRecord {
  id: number
  tenant_id: number
  claim_id: number
  api_key_id: number
  user_id: number
  provider_account_id?: number | null
  provider: string
  attempt_seq: number
  tokens_input: number
  tokens_output: number
  cache_creation_tokens: number
  cache_read_tokens: number
  /** 实际成本(十进制字符串,原样渲染)。 */
  actual_cost: string
  end_class: string
  usage_source: string
  pending_reconciliation: boolean
  stream_state: number
  delivered_token_count: number
  stream_terminated_reason?: string | null
  requested_at?: string | null
  created_at?: string | null
  requested_model: string
  upstream_model: string
  stream: boolean
  settlement_source: string
  pool_id?: number | null
  request_id: string
  /** 信任链校验态(trust.ResponseStatus);missing 表示无审计账本条目。 */
  trust_status: string
  ip_address?: string | null
  user_agent?: string | null
  client_tool?: string | null
}

/*
 * 计费 claim 台账记录。字段镜像 ListBillingClaimsRow(observability.sql.go:505,identityRow 原样直出)。
 * predicted_cost 为十进制字符串(decimal.Decimal,永不 null);actual_cost 为十进制字符串或 null
 * (decimal.NullDecimal:claim 尚未结算时 null)。created_at 永远有值,settled_at 未结算时为 null。
 */
export interface BillingClaim {
  id: number
  tenant_id: number
  idempotency_key: string
  api_key_id: number
  user_id: number
  logical_request_id: string
  endpoint_family: string
  requested_model: string
  pool_id?: number | null
  provider_account_id?: number | null
  attempt_seq: number
  /** 预扣成本(十进制字符串,原样渲染)。 */
  predicted_cost: string
  /** 实际结算成本(十进制字符串或 null;null=未结算)。 */
  actual_cost?: string | null
  currency_code: string
  /** claim 状态(后端自由字符串过滤,常见 pending/committed/aborted/settled)。 */
  status: string
  aborted_reason?: string | null
  created_at?: string | null
  settled_at?: string | null
  provider?: string | null
}

export type UsageListResponse = ObsListResponse<UsageRecord>
export type ClaimListResponse = ObsListResponse<BillingClaim>

/** 重算请求只有两种互斥范围；后端不接收原因或客户端幂等键。 */
export type RepriceRequest =
  | { usage_record_id: number; dry_run: boolean }
  | { tenant_id: number; from: string; to: string; limit: number; dry_run: boolean }

export interface RepriceSummary {
  total: number
  would_apply: number
  repriced: number
  already_repriced: number
  skipped: number
  failed: number
}

export interface RepriceItem {
  usage_record_id: number
  tenant_id: number
  status: string
  skipped_reason?: string
  error_code?: string
  error_message?: string
  original_cost: string
  authoritative_cost: string
  cost_delta: string
  pricing_source?: string
}

export interface RepriceResponse {
  object: 'billing_reprice_report'
  dry_run: boolean
  items: RepriceItem[]
  summary: RepriceSummary
}

export type RepriceScope = 'record' | 'window'

/** reason 是前端确认信息，不能伪装成后端已留存的审计字段。 */
export interface RepriceForm {
  scope: RepriceScope
  usageRecordId: string
  tenantId: string
  from: string
  to: string
  limit: string
  reason: string
  acknowledged: boolean
}

/** 原始用量过滤条件(全部可选);空串视为不过滤。 */
export interface UsageFilters {
  provider: string
  model: string
  poolId: string
  apiKeyId: string
  providerAccountId: string
  /** 结局:''(=all)/ success / error。 */
  outcome: string
  /** 仅看待对账(pending_reconciliation=true)的记录。 */
  pendingOnly: boolean
  from: string
  to: string
}

export const EMPTY_USAGE_FILTERS: UsageFilters = {
  provider: '',
  model: '',
  poolId: '',
  apiKeyId: '',
  providerAccountId: '',
  outcome: '',
  pendingOnly: false,
  from: '',
  to: '',
}

/** claim 台账过滤条件(全部可选);空串视为不过滤。 */
export interface ClaimFilters {
  status: string
  provider: string
  model: string
  poolId: string
  apiKeyId: string
  providerAccountId: string
  from: string
  to: string
}

export const EMPTY_CLAIM_FILTERS: ClaimFilters = {
  status: '',
  provider: '',
  model: '',
  poolId: '',
  apiKeyId: '',
  providerAccountId: '',
  from: '',
  to: '',
}
