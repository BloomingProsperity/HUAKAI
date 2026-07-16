import type { AuditEvent, AuditFilters } from './types'

/*
 * 审计查看器纯逻辑(可单测):过滤条件 → query 参数构造(空串省略),严重度配色,本地时间 → RFC3339。
 */
export type QueryValue = string | number | undefined

/**
 * 据过滤条件构造 query 参数对象;空白字段一律省略(不下发 event_class=""),
 * 避免空过滤被后端当作显式空字符串匹配。from/to 接受 datetime-local 值,转 ISO。
 */
export function buildAuditQuery(filters: AuditFilters, cursor?: string): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = {}
  const put = (key: string, raw: string) => {
    const v = raw.trim()
    if (v) q[key] = v
  }
  put('event_class', filters.eventClass)
  put('event_type', filters.eventType)
  put('severity', filters.severity)
  put('actor_id', filters.actorId)
  const from = toIso(filters.from)
  if (from) q.from = from
  const to = toIso(filters.to)
  if (to) q.to = to
  if (cursor && cursor.trim()) q.cursor = cursor.trim()
  return q
}

/** datetime-local(无时区)→ ISO8601;空串/非法 → 空串(调用方据此省略)。 */
export function toIso(local: string): string {
  const v = local.trim()
  if (!v) return ''
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

/*
 * 审计导出 URL 构造(纯逻辑,可单测)。后端 GET /v1/audit/export 两种过滤【互斥】:
 *   - 时间段:from + to(两者皆必填,RFC3339);或
 *   - request_ids:逗号分隔。
 * 二者不可同时给(后端 parseExportFilter 会 400 ambiguous_export_filter),也不可都不给。
 * 这里在前端先行收敛:request_ids 优先;否则要求 from/to 同时存在。
 * 真码:backend/internal/auditexporthttp/handler.go:216(parseExportFilter)。
 */
export interface AuditExportFilter {
  /** 起(本地 datetime-local 值);与 to 成对使用。 */
  from: string
  /** 止(本地 datetime-local 值)。 */
  to: string
  /** 指定 request_id 列表(优先于时间段)。 */
  requestIds?: string[]
}

/** 构造 /v1/audit/export 的查询参数;非法组合抛错(由调用方提示),绝不下发歧义参数。 */
export function buildExportQuery(filter: AuditExportFilter): Record<string, string> {
  const ids = (filter.requestIds ?? []).map((s) => s.trim()).filter(Boolean)
  if (ids.length > 0) {
    return { request_ids: ids.join(',') }
  }
  const from = toIso(filter.from)
  const to = toIso(filter.to)
  if (!from || !to) {
    throw new Error('导出需同时指定起止时间,或改用 request_ids')
  }
  return { from, to }
}

/** 把查询参数对象拼到路径后(已 encode);空对象返回原路径。 */
export function appendQuery(path: string, query: Record<string, string>): string {
  const parts = Object.entries(query).map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
  return parts.length ? `${path}?${parts.join('&')}` : path
}

export type SeverityTone = 'ok' | 'info' | 'warn' | 'danger' | 'muted'

/** 严重度 → 配色档:critical/error 危险,warn 警告,info 信息,其余中性。 */
export function severityTone(severity: string): SeverityTone {
  switch (severity.toLowerCase()) {
    case 'critical':
    case 'error':
      return 'danger'
    case 'warn':
    case 'warning':
      return 'warn'
    case 'info':
      return 'info'
    default:
      return 'muted'
  }
}

export interface AuditTableRow {
  id: number
  createdAt: string
  eventType: string
  eventClass: string
  severity: string
  actor: string
  reason: string
  requestID: string | null
  requestIDLabel: string
  detail: Record<string, unknown>
  source: AuditEvent
}

/** 把审计事件映射为稳定的表格展示模型，不改变原事件与敏感载荷。 */
export function mapAuditTableRows(events: AuditEvent[]): AuditTableRow[] {
  return events.map((event) => ({
    id: event.id,
    createdAt: formatAuditTime(event.created_at),
    eventType: event.event_type,
    eventClass: event.event_class,
    severity: event.severity || '—',
    actor: auditActorLabel(event),
    reason: event.reason || '—',
    requestID: event.request_id ?? null,
    requestIDLabel: event.request_id ? compactRequestID(event.request_id) : '—',
    detail: auditDetail(event),
    source: event,
  }))
}

function auditActorLabel(event: AuditEvent): string {
  if (event.actor_id == null && !event.actor_role) return '系统'
  const role = event.actor_role || ''
  const id = event.actor_id != null ? `#${event.actor_id}` : ''
  return [role, id].filter(Boolean).join(' ') || '—'
}

function auditDetail(event: AuditEvent): Record<string, unknown> {
  return {
    id: event.id,
    tenant_id: event.tenant_id,
    ledger_id: event.ledger_id,
    claim_id: event.claim_id,
    provider_account_id: event.provider_account_id,
    pool_group_id: event.pool_group_id,
    request_id: event.request_id,
    payload: event.payload,
  }
}

function compactRequestID(value: string): string {
  return value.length > 14 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value
}

function formatAuditTime(value: string): string {
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? value : time.toLocaleString('zh-CN', { hour12: false })
}
