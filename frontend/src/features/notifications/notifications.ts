import type { BadgeTone } from '../../ui/StatusBadge'
import type { UserNotification } from './types'

/*
 * 站内信纯逻辑(与 React/网络解耦,便于变异测试):未读判定、严重度配色/中文名、未读计数。
 */

/** 是否未读:read_at 为空(null/undefined/空串)即未读。 */
export function isUnread(n: Pick<UserNotification, 'read_at'>): boolean {
  return !n.read_at || n.read_at.trim() === ''
}

/** 列表内未读条数(本地角标兜底,优先用后端 unread-count)。 */
export function countUnread(items: Pick<UserNotification, 'read_at'>[]): number {
  // 判别核心:只数未读。变异(去掉 filter 直接 length)→ 已读也计入 → RED。
  return items.filter(isUnread).length
}

/** 严重度 → 徽章配色。info=蓝、warning=黄、critical=红,其余兜底灰。 */
export function severityTone(severity: string): BadgeTone {
  switch (severity.trim().toLowerCase()) {
    case 'info':
      return 'info'
    case 'warning':
      return 'warn'
    case 'critical':
      return 'danger'
    default:
      return 'muted'
  }
}

/** 严重度中文名。 */
export function severityLabel(severity: string): string {
  switch (severity.trim().toLowerCase()) {
    case 'info':
      return '通知'
    case 'warning':
      return '提醒'
    case 'critical':
      return '重要'
    default:
      return severity || '通知'
  }
}
