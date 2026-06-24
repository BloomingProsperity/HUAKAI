/*
 * 用量与配额前端类型 —— 镜像后端 DTO(均 session 鉴权,/v1/me/*):
 *  - 配额窗口  GET /v1/me/quota                    → mequotahttp.windowView
 *  - Key 用量  GET /v1/me/keys/{id}/usage-summary  → usageanalyticshttp.keyUsageSummaryResponse
 * 注意:/v1/me/usage(请求日志)是 API key 鉴权、session 不可达,故本页用 quota + 按 key 汇总。
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
