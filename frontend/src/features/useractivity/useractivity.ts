import type { UserAuditEvent } from './types'

/*
 * 用户安全日志的纯逻辑(可单测):动作/结果的中文化与配色、分页推进、是否还有下一页。
 * action/outcome 是后端自由字符串(无固定枚举),这里对已知常见值给中文标签,未知值做兜底人性化
 * (下划线转空格),绝不丢信息。配色只输出语义档,由 StatusBadge 落色 token。
 */

/** 分页:后端默认 50、上限 200(userauditlog/store.go:26)。前端取 50。 */
export const PAGE_LIMIT = 50

/** 已知动作 → 中文标签。覆盖用户自己日志里常见的 Key / 认证类事件;未命中走 humanizeAction。 */
const ACTION_LABELS: Record<string, string> = {
  issue_api_key: '签发 API Key',
  revoke_api_key: '撤销 API Key',
  list_api_keys: '查看 API Key 列表',
  reset_passkey: '重置通行密钥',
  force_disable_2fa: '关闭两步验证',
  enable_2fa: '开启两步验证',
  disable_2fa: '关闭两步验证',
  login: '登录',
  logout: '登出',
  password_change: '修改密码',
  update_billing_settings: '更新计费设置',
  set_user_remark: '修改备注',
}

/** 未知动作兜底人性化:下划线/点转空格,首字母大写,保留原信息不丢。 */
export function humanizeAction(action: string): string {
  const v = action.trim()
  if (!v) return '未知动作'
  return v
    .replace(/[._]+/g, ' ')
    .replace(/\b\w/g, (c) => c.toUpperCase())
}

export function actionLabel(action: string): string {
  return ACTION_LABELS[action.trim()] ?? humanizeAction(action)
}

export type Tone = 'ok' | 'warn' | 'danger' | 'muted' | 'info'

/**
 * 结果配色:成功类 → ok;失败/拒绝/错误类 → danger;告警/限流类 → warn;其余中性。
 * 大小写不敏感;子串匹配以容纳 "failure"/"failed"/"denied" 等变体。
 * 权威取值(userauditlog/store.go:20):committed(成功)/denied/error;另留 success 等防御别名。
 */
export function outcomeTone(outcome: string): Tone {
  const v = outcome.trim().toLowerCase()
  if (!v) return 'muted'
  if (v === 'committed' || v === 'success' || v === 'ok' || v === 'allowed' || v === 'granted') return 'ok'
  if (v.includes('fail') || v.includes('deny') || v.includes('denied') || v.includes('error') || v.includes('reject')) {
    return 'danger'
  }
  if (v.includes('warn') || v.includes('limit') || v.includes('throttle')) return 'warn'
  return 'muted'
}

/** 已知结果 → 中文标签;未知原样(大小写保留)。committed 是后端权威成功值。 */
const OUTCOME_LABELS: Record<string, string> = {
  committed: '成功',
  success: '成功',
  ok: '成功',
  failure: '失败',
  failed: '失败',
  denied: '已拒绝',
  deny: '已拒绝',
  error: '错误',
  allowed: '已放行',
  granted: '已授权',
  rejected: '已拒绝',
}

export function outcomeLabel(outcome: string): string {
  const v = outcome.trim()
  if (!v) return '—'
  return OUTCOME_LABELS[v.toLowerCase()] ?? v
}

/**
 * 是否可能还有下一页:本页返回条数等于请求 limit 时,后端可能还有更多(无总数,只能据此推断)。
 * 返回少于 limit(含 0)说明已到末页。
 */
export function hasMore(returnedCount: number, limit: number): boolean {
  return returnedCount >= limit && limit > 0
}

/** 推进到下一页 offset。 */
export function nextOffset(offset: number, limit: number): number {
  return offset + limit
}

export interface ActivityTableRow {
  id: number
  occurredAt: string
  action: string
  outcome: string
  outcomeTone: Tone
  keyPrefix: string
  reason: string
  requestID: string
}

/** 用户审计事件到只读表行的纯映射；请求 ID 与失败原因保留用于追踪。 */
export function mapActivityRows(events: UserAuditEvent[]): ActivityTableRow[] {
  return events.map((event) => ({
    id: event.id,
    occurredAt: formatTimestamp(event.occurred_at),
    action: actionLabel(event.action),
    outcome: outcomeLabel(event.outcome),
    outcomeTone: outcomeTone(event.outcome),
    keyPrefix: event.key_prefix ? `${event.key_prefix}…` : '—',
    reason: event.reason || '—',
    requestID: event.request_id || '—',
  }))
}

/** RFC3339(Nano)→ 本地可读串(24 小时制)。非法或空值保留诊断信息。 */
export function formatTimestamp(iso: string): string {
  if (!iso) return '—'
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString('zh-CN', { hour12: false })
}
