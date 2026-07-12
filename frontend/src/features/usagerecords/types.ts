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

/*
 * ── 签名收据 / 验签 / 我的争议(session 只读) ────────────────────────────────
 * 镜像后端这几条只读端点的 DTO(均 session 鉴权,routes.go:174-184 挂 SessionMiddleware,
 * 身份由会话派生,前端不传用户标识):
 *   GET  /v1/receipts/{request_id}                 单次签名成本收据(UserCostReceipt)
 *   POST /v1/receipts/{request_id}/verify          收据验签(只读密码学校验,空 body=验存储签名收据)
 *   GET  /v1/me/disputes                            我的争议列表(只读列本人争议)
 * 注:逐请求成本端点 /v1/generation 是 API-key(inboundAuth)鉴权、非 session 不可达,前端不接;
 *     成本/用量明细复用列表行 UsageRecord(/v1/me/usage-records)自带数据。
 * 真码:backend/internal/gatewayhttp/cost_receipt_handler.go:59/84/101/148、
 *       backend/internal/controlhttp/dispute_handler.go:62/116、backend/cmd/gateway/routes.go:175/178/184。
 */

/** 收据里的成本明细(UserReceiptCost,cost_receipt_handler.go:75)。token/微美分均为整数。 */
export interface UserReceiptCost {
  model: string
  input_tokens: number
  output_tokens: number
  cached_tokens: number
  cost_total_micro_usd: number
  rate_table_snapshot_id: number
}

/** 单次签名成本收据(UserCostReceipt,cost_receipt_handler.go:59)。canonical_hash/signature 用于验签。 */
export interface UserCostReceipt {
  schema_version: string
  request_id: string
  receipt_sequence: number
  tenant_id?: number
  tenant_scope_ref: string
  occurred_at: string
  cost: UserReceiptCost
  validation_state: string
  verdict: string
  adjustment_refs: string[]
  canonical_hash: string
  signature: string
  pubkey_fingerprint: string
}

/** 验签响应(receiptVerifyResponse,cost_receipt_handler.go:84)。valid=整体可信;各字段供取证展示。 */
export interface ReceiptVerifyResponse {
  valid: boolean
  status?: string
  signature_valid: boolean
  key_status: string
  reason?: string
  canonical_hash?: string
  schema_version?: string
  age_seconds: number
  receipt_sequence: number
  verdict?: string
  delta_micro_usd?: number
  fields_mismatch?: string[]
  refund_event_id?: number
  supported_versions?: string[]
}

/** 单条争议(disputeView,dispute_handler.go:62)。resolved_at 仅已处理时出现。 */
export interface Dispute {
  id: number
  dispute_id: string
  tenant_id: number
  user_id: number
  request_id: string
  reason: string
  status: string
  operator_note?: string
  created_at: string
  resolved_at?: string
}

/** 我的争议列表响应(NewListUserDisputesHandler,dispute_handler.go:135)。 */
export interface DisputesResponse {
  disputes: Dispute[]
}

/**
 * 发起争议响应(NewCreateDisputeHandler,dispute_handler.go:112)。
 * 后端回 201 Created + {"dispute": disputeView};只建 pending 记录,裁决/退款走 admin 侧、不立即动钱。
 */
export interface CreateDisputeResponse {
  dispute: Dispute
}
