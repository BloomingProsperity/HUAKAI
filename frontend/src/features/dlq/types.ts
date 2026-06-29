/*
 * 死信队列(DLQ)运营台 —— 数据类型。
 *
 * 字段形态以后端为权威:列表/重放统一走 mapDLQRecord 序列化。
 * 见 backend/internal/gatewayhttp/admin_dlq_handler.go:114-144(mapDLQRecord)
 * 与 backend/internal/dlq/types.go:87-111(Record 结构体)。
 *
 * 注意:handler 用 map[string]any 显式拼装,字段集合比 Go 的 Record JSON tag 更全
 * (含 replay_attempts / failure_reason / next_retry_at / replica_* / source_* 等),
 * 时间字段统一是 RFC3339Nano 字符串(无效时为空串 "")。payload 是原始 JSON 对象。
 */

/** 单条死信记录。字段镜像 admin_dlq_handler.go:119-143 的 map 拼装。 */
export interface DlqRecord {
  id: number
  tenant_id: number
  /** claim 关联 id;后端可能为 null(Record.ClaimID 是 *int64)。 */
  claim_id: number | null
  /** 事件类型(handler 取值集合),见 EVENT_KINDS。 */
  event_kind: string
  /** 泳道:HIGH / MED / LOW。 */
  lane: string
  /** 处理状态:pending / inflight / delivered / operator_review / dlq / quarantined。 */
  status: string
  /** 原始事件载荷(任意 JSON 对象;空时后端回 {})。 */
  payload: unknown
  failure_reason: string
  /** 首次失败时间(RFC3339Nano)。 */
  failure_at: string
  /** 已重放尝试次数。 */
  replay_attempts: number
  /** 最近一次重放时间(无效时空串)。 */
  last_replay_at: string
  /** 投递成功时间(无效时空串)。 */
  replayed_at: string
  /** 重放失败原因(后端可能为 null)。 */
  replay_failure_reason: string | null
  next_retry_at: string
  /** 租约持有者(后端可能为 null)。 */
  lease_owner: string | null
  lease_until: string
  replica_status: string
  replica_target: string
  replica_committed_at: string
  idempotency_key: string
  source_table: string
  /** 源表记录 id(后端可能为 null)。 */
  source_id: number | null
  /** 运维已审阅时间(无效时空串)。 */
  operator_review_at: string
}

/** GET /admin/v1/dlq/{handler} 响应。 */
export interface DlqListResponse {
  items: DlqRecord[]
}

/** POST .../replay 响应。 */
export interface DlqReplayResponse {
  item: DlqRecord
  replayed: boolean
}

/** handler 路径参数取值集合,镜像 backend/internal/dlq/types.go:14-30 的 EventKind 常量。 */
export const EVENT_KINDS = [
  'usage_record',
  'billing_event_replica',
  'audit_event_replica',
  'audit_mismatch_refund',
  'audit_ledger_entry',
  'account_health',
  'metrics',
  'post_delivery_settlement',
  'cost_receipt_append',
] as const

export type EventKind = (typeof EVENT_KINDS)[number]

/** 可选的状态筛选取值,镜像 backend/internal/dlq/types.go:44-49 的 Status 常量。空串=不筛。 */
export const STATUS_FILTERS = [
  '',
  'pending',
  'inflight',
  'delivered',
  'operator_review',
  'dlq',
  'quarantined',
] as const

export type StatusFilter = (typeof STATUS_FILTERS)[number]
