/*
 * 审计事件查看器(运维台/安全)前端类型 —— 镜像 admin_observability_handler 的 JSON。
 * 端点:GET /admin/v1/audit-events(admin token 鉴权,只读)。
 */
export interface AuditEvent {
  id: number
  tenant_id?: number | null
  event_class: string
  event_type: string
  severity: string
  ledger_id?: number | null
  claim_id?: string | null
  provider_account_id?: number | null
  pool_group_id?: number | null
  request_id?: string | null
  actor_id?: number | null
  actor_role?: string | null
  reason?: string | null
  payload?: unknown
  created_at: string
}

export interface AuditListResponse {
  items: AuditEvent[]
  next_cursor: string
  total: number
}

/** 审计过滤条件(全部可选);空串视为不过滤。 */
export interface AuditFilters {
  eventClass: string
  eventType: string
  severity: string
  actorId: string
  from: string
  to: string
}

export const EMPTY_AUDIT_FILTERS: AuditFilters = {
  eventClass: '',
  eventType: '',
  severity: '',
  actorId: '',
  from: '',
  to: '',
}
