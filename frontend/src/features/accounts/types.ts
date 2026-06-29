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

/*
 * 账号诊断/健康/上游模型 DTO —— 镜像后端 adminhttp 三个 handler 的 JSON 形态:
 * - provider_account_test_handler.go:providerAccountTestResponseBody
 * - provider_account_health_handler.go:providerAccountHealthResponseBody
 * - provider_account_upstream_models_handler.go:upstreamModelsListResponse
 */

/** POST /{id}/test 凭证试运行结果。ok=true 即连通;error_class 为粗粒度枚举(失败时)。 */
export interface AccountTestResult {
  ok: boolean
  error_class: string | null
  message: string
}

/** GET /{id}/health 实时健康面板(字段名严格对齐 handler,非 recentReqRing)。 */
export interface AccountHealth {
  id: number
  health_state: string
  health_state_until?: string | null
  last_probe_latency_ms: number | null
  last_probe_at: string | null
  model_sync_last_check_at: string | null
  session_window_5h_start: string | null
  session_window_5h_end: string | null
  session_window_5h_status: string | null
  last_refresh_at: string | null
  last_refresh_outcome: string | null
  failure_class: string | null
  failure_count: number
  enabled: boolean
  requires_action: boolean
  updated_at: string
  /** 进程内近期请求计数。ring 为 nil 或无数据时后端省略该字段。 */
  recent_requests?: {
    total: number
    success: number
    failure: number
    last_at?: string
  }
}

/** GET /{id}/upstream-models 上游可用模型清单(OpenAI 兼容 /v1/models 去重后)。 */
export interface UpstreamModelsResult {
  models: string[]
  count: number
}

/** POST /bulk-by-tag 批量调参的响应:受影响账号 ID + 计数。 */
export interface BulkByTagResult {
  affected_ids: number[]
  count: number
}

/**
 * DELETE /{id} 硬删账号的响应:{id, deleted}。
 * 真码:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:695。
 */
export interface DeleteAccountResult {
  id: number
  deleted: boolean
}

/*
 * ---- 账号 TLS 指纹 profile 绑定 DTO ----
 * 对应 backend/internal/accountfphttp/fingerprint_handler.go:
 *   PATCH /{id}/fingerprint-profile 响应 {id, tls_fingerprint_profile_id}(:117)。
 * 注意:账号详情/列表的 providerAccountResponse **不**暴露当前绑定的 profile id(已核源码),
 * 故初次进入详情页时"当前绑定"未知;仅在本次保存成功后由本响应回显新值。
 */
export interface FingerprintBindResult {
  id: number
  /** 绑定后的 profile id;解绑(回内置默认)时为 null。 */
  tls_fingerprint_profile_id: number | null
}

/**
 * 绑定下拉用的轻量 profile 选项(取自 tlsfphttp 列表的子集)。
 * 来源:GET /v1/admin/tls-fingerprint-profiles 的 items[](tlsfphttp/handler.go:110)。
 * status 为 active / disabled / drift_detected;UI 据此标注(disabled/drift 仍可选但提示)。
 */
export interface FingerprintProfileOption {
  id: number
  name: string
  status: string
}
