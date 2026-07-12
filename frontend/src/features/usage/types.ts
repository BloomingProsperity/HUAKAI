/*
 * 用量与配额前端类型 —— 镜像后端 DTO:
 *  - 配额窗口  GET /v1/me/quota                    → mequotahttp.windowView
 *  - Key 用量  GET /v1/me/keys/{id}/usage-summary  → usageanalyticshttp.keyUsageSummaryResponse
 *  - Key 逐笔  GET /v1/me/usage                     → meusagehttp.listResponse
 *  - Key 时序  GET /v1/me/analytics/time-series     → usageanalyticshttp.timeSeriesResponse
 *  - 单笔下钻  GET /v1/generation?id=               → meusagehttp.usageRecord
 * 后三条只接受 API Key Bearer，不能使用 session token。
 */

export interface QuotaWindow {
  metric: string
  window_kind: string
  cap: string
  consumed: string
  remaining: string
  overage: string
  request_count: number
  window_start: string
  window_end: string
}

export interface QuotaResponse {
  items: QuotaWindow[]
}

export interface KeyUsageSummary {
  api_key_id: number
  total_cost: string
  total_tokens_input: number
  total_tokens_output: number
  total_cache_read_tokens: number
  total_cache_creation_tokens: number
  request_count: number
  from?: string | null
  to?: string | null
}

export interface KeyUsageTokens {
  input: number
  output: number
  cache_creation?: number
  cache_read?: number
}

export interface KeyUsageVerifyHint {
  ledger_id?: string
  trust_verify_path: string
  trust_verify_method: string
  audit_verify_path?: string
  audit_verify_method?: string
  request_id?: string
  tenant_scope_ref?: string
}

/** Key 作用域内的单条请求记录。actual_cost 为定点十进制字符串。 */
export interface KeyUsageRecord {
  requested_model: string
  upstream_model: string
  actual_cost: string
  tokens: KeyUsageTokens
  provider?: string
  provider_account_id?: number
  ledger_id: string
  verify_hint: KeyUsageVerifyHint
  created_at: string
  status: string
  request_id?: string
  stream: boolean
  stream_terminated_reason?: string
  requested_at?: string
  /** 端到端时延毫秒(结算-请求);后端在时间缺失/负值时省略。 */
  latency_ms?: number
}

export interface KeyUsageRecordsResponse {
  items: KeyUsageRecord[]
  next_cursor: string
}

export type KeyUsageGranularity = 'day' | 'week' | 'month'

export interface KeyUsageAggregateTokens {
  input: number
  output: number
  cache_read: number
  cache_creation: number
}

export interface KeyUsageTimeSeriesPoint {
  day: string
  requested_model: string
  total_cost: string
  tokens: KeyUsageAggregateTokens
  request_count: number
}

export interface KeyUsageTimeSeriesResponse {
  items: KeyUsageTimeSeriesPoint[]
  period: {
    from: string
    to: string
  }
}
