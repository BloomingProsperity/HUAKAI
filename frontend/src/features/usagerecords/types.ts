/*
 * 用量明细 / 请求日志(用户门户 · 用量与配额)前端类型 —— 镜像 meusagehttp 的 session 端点 DTO。
 * 端点(session 鉴权,身份从会话上下文派生、不读请求体里的用户标识):
 *   GET /v1/me/usage-records?limit=&from=&to=&cursor=   当前用户跨全部 key 的逐请求用量
 * 真码:backend/internal/meusagehttp/session_handler.go:19、handler.go:49-71(DTO)、
 *       backend/cmd/gateway/routes.go:192。游标分页(next_cursor 不透明 base64,空串=末页)。
 */

/** 单请求 token 拆分(usageTokens,handler.go)。input/output 恒有;cache 仅非零时出现。 */
export interface UsageTokens {
  input: number
  output: number
  cache_creation?: number
  cache_read?: number
}

/** 单条用量记录(usageRecord,handler.go:54)。actual_cost 是定点小数串;时间为 RFC3339Nano。 */
export interface UsageRecord {
  requested_model: string
  upstream_model: string
  actual_cost: string
  tokens: UsageTokens
  provider?: string
  provider_account_id?: number | null
  ledger_id: string
  created_at: string
  status: string
  request_id?: string
  stream: boolean
  stream_terminated_reason?: string
  requested_at?: string
}

/** 列表响应(listResponse,handler.go:49)。next_cursor 空串表示没有下一页。 */
export interface UsageRecordsResponse {
  items: UsageRecord[]
  next_cursor: string
}
