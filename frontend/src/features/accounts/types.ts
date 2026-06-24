/*
 * 上游账号(provider account)前端类型 —— 镜像后端 providerAccountResponse 的 JSON 形态
 * (backend/internal/gatewayhttp/admin_pool_accounts_handler.go 的 providerAccountResponse)。
 * 只取列表页用得到的字段;详情页扩展字段后续切片再补。
 */

/** 账号运行态枚举(state_filter 合法值,与后端一致)。 */
export type AccountState =
  | 'active'
  | 'error'
  | 'disabled'
  | 'rate_limited'
  | 'overloaded'
  | 'temp_unschedulable'

export interface ProviderAccount {
  id: number
  tenant_id: number
  provider_id: number
  channel_id: number
  name: string
  account_type: string
  enabled: boolean
  expires_at: string | null
  health_state: string
  credential_state: string
  cap_concurrency: number
  in_flight_count: number
  priority: number
  static_weight: number
  probe_model: string | null
  tags: string[]
  last_dispatch_at: string | null
  last_probe_latency_ms: number | null
  last_probe_at: string | null
  model_allow_list: string[]
  capability_flags: string[]
  rate_limited_at: string | null
  rate_limit_reset_at: string | null
  rate_limit_reason: string | null
  overload_until: string | null
  temp_unschedulable_until: string | null
  token_version: number
  last_refresh_at: string | null
  last_refresh_outcome: string | null
  oauth_endpoint_health?: string
}

/** 列表响应:{ items, page } —— 与后端 providerAccountListResponse 一致。 */
export interface ProviderAccountListResponse {
  items: ProviderAccount[]
  page: {
    cursor: string | null
    next_cursor: string | null
    has_more: boolean
  }
}
