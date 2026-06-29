import type { BadgeTone } from '../../ui/StatusBadge'
import type { SessionFamily } from './sessionsTypes'

/*
 * 活跃会话纯逻辑(可单测,无 IO 无 DOM):
 *   - 族状态 → 中文标签 + 徽章语气;
 *   - 是否可撤销(仅 active 族可撤销);
 *   - device_info → 简明设备摘要(浏览器/OS/UA,缺失兜底);
 *   - 列表排序:活跃在前,再按最近活跃时间倒序(让用户先看到正在用的设备)。
 * 状态取值与后端 usersession FamilyStatus 对齐(types.go:10)。
 */

/** 族状态 → 中文标签;未知原样回显。 */
export function familyStatusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '活跃'
    case 'revoked':
      return '已撤销'
    case 'expired':
      return '已过期'
    case 'suspicious':
      return '可疑'
    case 'replaced':
      return '已轮换'
    default:
      return status || '未知'
  }
}

/** 族状态 → 徽章语气。active=ok,suspicious=warn(注意),revoked/expired=muted,replaced=info。 */
export function familyStatusTone(status: string): BadgeTone {
  switch (status) {
    case 'active':
      return 'ok'
    case 'suspicious':
      return 'warn'
    case 'replaced':
      return 'info'
    case 'revoked':
    case 'expired':
      return 'muted'
    default:
      return 'muted'
  }
}

/**
 * 是否可撤销:仅 active 族能撤销(已撤销/过期/轮换的族再撤销无意义,后端也不会有效)。
 * 判别核心:非 active 必须返回 false(变异成恒 true → 对已失效族露出撤销按钮 → RED)。
 */
export function canRevoke(family: Pick<SessionFamily, 'status'>): boolean {
  return family.status === 'active'
}

/**
 * 从 device_info 提炼简明设备摘要。后端把 user_agent / ip 等放在 device_info map 里(形态不固定),
 * 这里尽力取常见键,缺失回落到「未知设备」。绝不抛错(脏数据不能让整页崩)。
 */
export function deviceSummary(family: Pick<SessionFamily, 'device_info' | 'ip_baseline'>): string {
  const info = family.device_info ?? undefined
  const parts: string[] = []
  if (info && typeof info === 'object') {
    const ua = pickString(info, ['user_agent', 'ua', 'agent'])
    const platform = pickString(info, ['platform', 'os'])
    const label = pickString(info, ['label', 'device', 'device_label'])
    if (label) parts.push(label)
    if (platform) parts.push(platform)
    if (ua && parts.length === 0) parts.push(shorten(ua))
  }
  if (parts.length === 0) parts.push('未知设备')
  const ip = (family.ip_baseline ?? '').trim()
  if (ip) parts.push(ip)
  return parts.join(' · ')
}

function pickString(obj: Record<string, unknown>, keys: string[]): string {
  for (const k of keys) {
    const v = obj[k]
    if (typeof v === 'string' && v.trim() !== '') return v.trim()
  }
  return ''
}

function shorten(s: string): string {
  return s.length > 48 ? `${s.slice(0, 45)}…` : s
}

/**
 * 列表排序:活跃族在前,同组内按最近活跃时间倒序。
 * 判别核心:active 必须排在非 active 之前(变异成不分组 → 已撤销族混在前面 → RED)。
 */
export function sortFamilies(families: SessionFamily[]): SessionFamily[] {
  return [...families].sort((a, b) => {
    const aActive = a.status === 'active' ? 0 : 1
    const bActive = b.status === 'active' ? 0 : 1
    if (aActive !== bActive) return aActive - bActive
    return tsValue(b.last_active_at) - tsValue(a.last_active_at)
  })
}

function tsValue(iso: string): number {
  const t = Date.parse(iso)
  return Number.isNaN(t) ? 0 : t
}
