import type { BadgeTone } from '../../ui/StatusBadge'
import type { ApiKeyView } from './types'

export const KEYS_PAGE_LIMIT = 100
export const KEYS_PAGE_LIMIT_OPTIONS = [25, 50, 100, 200] as const

export interface KeyStatView {
  label: string
  value: string
  hint: string
  tone: 'default' | 'danger' | 'warn' | 'ok'
}

/** 列表响应到统计卡的纯映射；总数取后端 count，状态数只统计当前页。 */
export function mapKeyStats(keys: ApiKeyView[], totalCount: number | null): KeyStatView[] {
  if (totalCount === null) {
    return [
      { label: '总数', value: '—', hint: '全部密钥总数暂不可用', tone: 'default' },
      { label: '活跃', value: '—', hint: '当前页口径 · 数据暂不可用', tone: 'ok' },
      { label: '已撤销', value: '—', hint: '当前页口径 · 数据暂不可用', tone: 'danger' },
    ]
  }

  const active = keys.filter((key) => key.status === 'active').length
  const revoked = keys.filter((key) => key.status === 'revoked').length
  return [
    {
      label: '总数',
      value: `${totalCount.toLocaleString('zh-CN')} 个`,
      hint: '全部密钥，不受当前分页影响',
      tone: 'default',
    },
    { label: '活跃', value: `${active.toLocaleString('zh-CN')} 个`, hint: '当前页口径', tone: 'ok' },
    { label: '已撤销', value: `${revoked.toLocaleString('zh-CN')} 个`, hint: '当前页口径', tone: 'danger' },
  ]
}

export interface KeyTableRow {
  id: number
  source: ApiKeyView
  name: string
  prefix: string
  status: string
  statusText: string
  statusTone: BadgeTone
  expiresAt: string
  lastUsedAt: string
  createdAt: string
}

/** 后端密钥项到表格行的纯映射；只保留脱敏前缀，不产生或拼接明文密钥。 */
export function mapKeyRows(keys: ApiKeyView[]): KeyTableRow[] {
  return keys.map((key) => ({
    id: key.api_key_id,
    source: key,
    name: key.name,
    prefix: key.key_prefix,
    status: key.status,
    statusText: keyStatusLabel(key.status),
    statusTone: keyStatusTone(key.status),
    expiresAt: formatKeyTimestamp(key.expires_at, '永不'),
    lastUsedAt: formatKeyTimestamp(key.last_used_at, '从未'),
    createdAt: formatKeyTimestamp(key.created_at, '—'),
  }))
}

export interface KeyPaginationView {
  page: number
  start: number
  end: number
  canPrevious: boolean
  canNext: boolean
  scopeText: string
}

/** offset 分页展示纯映射；后端 count 可用时以全量总数决定是否存在下一页。 */
export function mapKeyPagination(input: {
  offset: number
  limit: number
  returnedCount: number
  totalCount: number | null
}): KeyPaginationView {
  const { offset, limit, returnedCount, totalCount } = input
  const safeLimit = limit > 0 ? limit : KEYS_PAGE_LIMIT
  const start = returnedCount > 0 ? offset + 1 : 0
  const end = returnedCount > 0 ? offset + returnedCount : 0
  const canNext = totalCount === null ? returnedCount >= safeLimit : end < totalCount
  const totalText = totalCount === null
    ? '总数暂不可用'
    : `共 ${totalCount.toLocaleString('zh-CN')} 个`

  return {
    page: Math.floor(offset / safeLimit) + 1,
    start,
    end,
    canPrevious: offset > 0,
    canNext,
    scopeText: `第 ${start}–${end} 条 · ${totalText}`,
  }
}

export function keyStatusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '活跃'
    case 'revoked':
      return '已撤销'
    case 'expired':
      return '已过期'
    default:
      return status
  }
}

export function keyStatusTone(status: string): BadgeTone {
  switch (status) {
    case 'active':
      return 'ok'
    case 'expired':
      return 'danger'
    default:
      return 'muted'
  }
}

function formatKeyTimestamp(iso: string | null | undefined, fallback: string): string {
  if (!iso) return fallback
  const date = new Date(iso)
  return Number.isNaN(date.getTime())
    ? fallback
    : date.toLocaleString('zh-CN', { hour12: false })
}
