import type { BadgeTone } from '../../ui/StatusBadge'
import { healthTone } from '../../ui/StatusBadge'
import type { AccountHealthSummary, ProviderAccount } from './types'

export interface AccountStatView {
  label: string
  value: string
  hint: string
  tone: 'default' | 'danger' | 'warn' | 'ok'
}

/** 全池健康聚合到统计卡的纯映射；需关注只使用 warn，避免把可恢复运维态误作故障。 */
export function mapAccountStats(summary: AccountHealthSummary): AccountStatView[] {
  return [
    { label: '账号总数', value: summary.total.toLocaleString('zh-CN'), hint: '全池真值 · 非分页', tone: 'default' },
    { label: '已启用', value: summary.enabled.toLocaleString('zh-CN'), hint: '当前可参与调度', tone: 'ok' },
    { label: '已停用', value: summary.disabled.toLocaleString('zh-CN'), hint: '全池停用账号', tone: 'default' },
    {
      label: '需关注',
      value: summary.needs_attention.toLocaleString('zh-CN'),
      hint: '非健康或已停用',
      tone: summary.needs_attention > 0 ? 'warn' : 'ok',
    },
  ]
}

export interface AccountTableRow {
  id: number
  source: ProviderAccount
  name: string
  tags: string[]
  accountType: string
  enabledText: string
  enabledTone: BadgeTone
  healthState: string
  healthTone: BadgeTone
  credentialState: string
  credentialTone: BadgeTone
  inFlightCount: number
  priority: number
  staticWeight: number
  capConcurrency: number
  lastDispatchAt: string
}

/** 后端账号项到十列表格行的纯映射；保留 source 供行内操作使用。 */
export function mapAccountRows(accounts: ProviderAccount[]): AccountTableRow[] {
  return accounts.map((account) => ({
    id: account.id,
    source: account,
    name: account.name,
    tags: account.tags ?? [],
    accountType: account.account_type,
    enabledText: account.enabled ? '已启用' : '已停用',
    enabledTone: account.enabled ? 'ok' : 'muted',
    healthState: account.health_state || '—',
    healthTone: healthTone(account.health_state),
    credentialState: account.credential_state || '—',
    credentialTone: accountCredentialTone(account.credential_state),
    inFlightCount: account.in_flight_count,
    priority: account.priority,
    staticWeight: account.static_weight,
    capConcurrency: account.cap_concurrency,
    lastDispatchAt: formatAccountTime(account.last_dispatch_at),
  }))
}

/** 名称与健康态都是当前游标页内过滤，精确匹配健康态以免伪造服务端筛选语义。 */
export function filterAccountRows(
  rows: AccountTableRow[],
  nameQuery: string,
  healthState: string,
): AccountTableRow[] {
  const normalizedName = nameQuery.trim().toLocaleLowerCase('zh-CN')
  return rows.filter((row) => {
    const matchesName = normalizedName === '' || row.name.toLocaleLowerCase('zh-CN').includes(normalizedName)
    const matchesHealth = healthState === '' || row.source.health_state === healthState
    return matchesName && matchesHealth
  })
}

export function accountCredentialTone(state: string): BadgeTone {
  switch (state) {
    case 'active':
    case 'valid':
      return 'ok'
    case 'expiring':
    case 'needs_rotation':
      return 'warn'
    case 'revoked':
    case 'expired':
    case 'invalid':
      return 'danger'
    default:
      return 'muted'
  }
}

export function formatAccountTime(iso: string | null): string {
  if (!iso) return '—'
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}
