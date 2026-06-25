/*
 * 概览页前端类型 —— 镜像后端 DTO(均 session 鉴权,/v1/me/* 与 /v1/api-keys)。
 *
 * 端点(动手前已核 backend 真码):
 *   - 配额窗口   GET /v1/me/quota                    → mequotahttp.windowView
 *     (routes.go:186,/v1/me 组内 SessionMiddleware,session 可达)
 *   - Key 用量   GET /v1/me/keys/{id}/usage-summary  → usageanalyticshttp.keyUsageSummaryResponse
 *     (routes.go:194,/v1/me 组内 session 可达)
 *   - Key 列表   GET /v1/api-keys                     → userkeyhttp.apiKeyListResponse
 *     (routes.go:324,SessionMiddleware,session 可达)
 *
 * 重要约束(真码核出):/v1/me/analytics/time-series(routes.go:133)挂的是 d.inboundAuth
 * = APIKeyResolver(hk_key Bearer 鉴权),不在 /v1/me 的 session 组里,session token 不可达。
 * 故概览的"用量时序简图"改用 session 可达的 per-key usage-summary 汇总成简图,与 usage 页同策略。
 */

/** 配额窗口项(mequotahttp.windowView)。cap/consumed 等是十进制字符串(可能带小数)。 */
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

/** per-key 用量汇总(usageanalyticshttp.keyUsageSummaryResponse)。 */
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

/** API Key 列表项(userkeyhttp.apiKeyView)的最小投影(本页只用到 id/name/status/key_prefix)。 */
export interface ApiKeyView {
  api_key_id: number
  name: string
  key_prefix: string
  status: string
}

export interface ApiKeyListResponse {
  api_keys: ApiKeyView[]
  count: number
}
