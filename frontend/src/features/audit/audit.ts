import type { AuditFilters } from './types'

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
