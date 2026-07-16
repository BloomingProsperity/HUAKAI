/*
 * 运营总览纯映射层：每个可视部件单独映射，网络失败与加载态留给页面处理。
 * 所有计算只消费真实响应；缺字段不补猜测值，调用方改走诚实空态。
 */
import type { AccountHealthSummary } from '../accounts/types'
import type { AlertEvent } from '../alerting/types'
import type { AuditEvent } from '../audit/types'
import type { ChannelHealthSummary } from '../channelhealth/types'
import type { LeaderboardEntry, OverviewResponse, OverviewTrendPoint } from '../ops/types'
import { fmtFractionPct, fmtInt } from '../ops/ops'
import type { QuotaPolicy } from '../quotapolicies/types'
import type { DonutSegment } from '../../ui/Donut'
import type { ResourceBadge } from '../../ui/ResourceCard'

export interface OverviewStat {
  key: string
  label: string
  value: string
  hint: string
  to: string
  icon: string
  tone?: 'default' | 'danger'
  sparkline?: number[]
}

export function gatewayAvailabilityStat(overview: OverviewResponse): OverviewStat {
  if (overview.totals.requests === 0) {
    return { key: 'availability', label: '网关可用率', value: '—', hint: '暂无请求', to: '/health', icon: '✓' }
  }
  return { key: 'availability', label: '网关可用率', value: fmtFractionPct(overview.totals.success_rate), hint: '近似值 · 按请求成功率', to: '/health', icon: '✓' }
}

export function requestVolumeStat(overview: OverviewResponse): OverviewStat {
  return { key: 'requests', label: '今日请求量', value: fmtInt(overview.totals.requests), hint: '滚动 24 小时', to: '/usage', icon: '↗', sparkline: overview.trend.map((point) => point.requests) }
}

/** “可用”排除被停用或健康态需关注的账号，避免把仅 enabled 误报成可服务。 */
export function availableAccountCount(summary: AccountHealthSummary): number {
  return Math.max(0, summary.total - summary.needs_attention)
}

export function accountStat(summary: AccountHealthSummary): OverviewStat {
  const available = availableAccountCount(summary)
  return { key: 'accounts', label: '活跃上游账号', value: `${fmtInt(available)}/${fmtInt(summary.total)}`, hint: `${fmtInt(available)} 可用 · ${fmtInt(summary.needs_attention)} 需关注`, to: '/accounts', icon: '◎', tone: summary.needs_attention > 0 ? 'danger' : 'default' }
}

export function modelStat(count: number): OverviewStat {
  return { key: 'models', label: '在线模型', value: fmtInt(count), hint: '按价目模型数', to: '/models', icon: '◇' }
}

export function firingAlertStat(events: AlertEvent[]): OverviewStat {
  const count = events.filter((event) => event.state === 'firing').length
  return { key: 'alerts', label: '异常告警', value: fmtInt(count), hint: count === 0 ? '当前无异常' : `${fmtInt(count)} 条待处理`, to: '/admin/alerting', icon: '!', tone: count > 0 ? 'danger' : 'default' }
}

export interface ResourceItem {
  key: string
  title: string
  value: string
  icon: string
  badges?: ResourceBadge[]
  action: { label: string; to: string }
}

export function modelResource(count: number): ResourceItem {
  return { key: 'models', title: '在线模型数', value: fmtInt(count), icon: '◇', badges: [{ label: '口径', value: '按价目模型数', tone: 'neutral' }], action: { label: '管理模型服务', to: '/models' } }
}

export function accountResource(summary: AccountHealthSummary): ResourceItem {
  const available = availableAccountCount(summary)
  return { key: 'accounts', title: '上游账号', value: `${fmtInt(available)}/${fmtInt(summary.total)}`, icon: '◎', badges: [{ label: '可用', value: fmtInt(available), tone: 'ok' }, { label: '不可用', value: fmtInt(summary.needs_attention), tone: summary.needs_attention > 0 ? 'danger' : 'neutral' }], action: { label: '管理上游账号', to: '/accounts' } }
}

export function abnormalPoolCount(summary: ChannelHealthSummary): number {
  return ['cooling_down', 'disabled', 'degraded'].reduce((total, state) => total + (summary.by_state[state] ?? 0), 0)
}

export function poolResource(inventoryCount: number, summary?: ChannelHealthSummary): ResourceItem {
  const available = summary?.by_state.active ?? 0
  const abnormal = summary ? abnormalPoolCount(summary) : 0
  const hasHealthReport = available + abnormal > 0
  return {
    key: 'pools',
    title: '账号池',
    value: fmtInt(inventoryCount),
    icon: '▦',
    badges: hasHealthReport
      ? [{ label: '可用', value: fmtInt(available), tone: 'ok' }, { label: '异常', value: fmtInt(abnormal), tone: abnormal > 0 ? 'danger' : 'neutral' }]
      : [{ label: '可用', value: '未上报', tone: 'neutral' }, { label: '异常', value: '未上报', tone: 'neutral' }],
    action: { label: '管理账号池', to: '/accounts?tab=pool' },
  }
}

export function quotaResource(policies: QuotaPolicy[]): ResourceItem {
  const enabled = policies.filter((policy) => policy.enabled).length
  const disabled = policies.length - enabled
  return { key: 'quota', title: '流量控制', value: fmtInt(policies.length), icon: '≋', badges: [{ label: '生效中', value: fmtInt(enabled), tone: 'ok' }, { label: '已停用', value: fmtInt(disabled), tone: disabled > 0 ? 'warn' : 'neutral' }], action: { label: '配置限流策略', to: '/routing' } }
}

export type PendingPriority = '高' | '中'
export interface PendingItem {
  key: string
  priority: PendingPriority
  title: string
  detail: string
  actionLabel: string
  to: string
}

export function pendingItems(events: AlertEvent[], accounts: AccountHealthSummary, pools: ChannelHealthSummary): PendingItem[] {
  const firing = events.filter((event) => event.state === 'firing').length
  const abnormalPools = abnormalPoolCount(pools)
  const rows: PendingItem[] = []
  if (firing > 0) rows.push({ key: 'alerts', priority: '高', title: '异常告警处理', detail: `${fmtInt(firing)} 条告警待处理`, actionLabel: '查看', to: '/admin/alerting' })
  if (accounts.needs_attention > 0) rows.push({ key: 'accounts', priority: '高', title: '上游账号不可用', detail: `${fmtInt(accounts.needs_attention)} 个账号不可用`, actionLabel: '处理', to: '/accounts?filter=down' })
  if (abnormalPools > 0) rows.push({ key: 'pools', priority: '中', title: '账号池异常', detail: `${fmtInt(abnormalPools)} 个账号池存在异常`, actionLabel: '处理', to: '/accounts?tab=pool' })
  return rows
}

export interface TrendPoint {
  label: string
  value: number
}

export function requestTrend(points: OverviewTrendPoint[]): TrendPoint[] {
  return points.map((point) => ({ label: point.day, value: point.requests }))
}

const DONUT_COLORS = ['var(--hk-primary-500)', 'var(--hk-info)', 'var(--hk-cat-purple)', 'var(--hk-cat-teal)', 'var(--hk-warn)', 'var(--hk-ink-300)']

export interface ModelDistribution {
  total: number
  segments: DonutSegment[]
}

export function modelDistribution(entries: LeaderboardEntry[], visible = 5): ModelDistribution {
  const clean = entries.filter((entry) => Number.isFinite(entry.request_count) && entry.request_count > 0)
  const total = clean.reduce((sum, entry) => sum + entry.request_count, 0)
  const head = clean.slice(0, visible)
  const other = clean.slice(visible).reduce((sum, entry) => sum + entry.request_count, 0)
  const segments = head.map((entry, index) => ({ label: entry.key, value: entry.request_count, color: DONUT_COLORS[index % DONUT_COLORS.length], to: `/usage?model=${encodeURIComponent(entry.key)}` }))
  if (other > 0) segments.push({ label: '其他', value: other, color: DONUT_COLORS[DONUT_COLORS.length - 1], to: '/usage' })
  return { total, segments }
}

export interface QuickLinkItem {
  label: string
  icon: string
  to: string
  badge?: number
}

export function quickLinks(firingCount: number): QuickLinkItem[] {
  return [
    { label: '新建上游账号', icon: '+', to: '/accounts' },
    { label: '创建账号池', icon: '▦', to: '/accounts?tab=pool' },
    { label: '配置限流', icon: '≋', to: '/routing' },
    { label: '查看审计日志', icon: '⌕', to: '/activity' },
    { label: '处理告警', icon: '!', to: '/admin/alerting', badge: firingCount > 0 ? firingCount : undefined },
    { label: '更多操作', icon: '…', to: '/ops' },
  ]
}

export function firingAlertCount(events: AlertEvent[]): number {
  return events.filter((event) => event.state === 'firing').length
}

export type AuditTone = 'ok' | 'info' | 'warn' | 'danger' | 'muted'
export interface AuditRow {
  id: number
  time: string
  type: string
  tone: AuditTone
  object: string
  actor: string
  detail: string
}

export function auditRows(events: AuditEvent[], limit: number): AuditRow[] {
  return events.slice(0, limit).map((event) => ({
    id: event.id,
    time: formatShortTime(event.created_at),
    type: event.event_type || event.event_class,
    tone: auditTone(event.severity),
    object: auditObject(event),
    actor: event.actor_id == null ? (event.actor_role || '系统') : `${event.actor_role || '操作人'} #${event.actor_id}`,
    detail: event.reason?.trim() || '—',
  }))
}

function auditObject(event: AuditEvent): string {
  if (event.provider_account_id != null) return `上游账号 #${event.provider_account_id}`
  if (event.pool_group_id != null) return `账号池 #${event.pool_group_id}`
  if (event.ledger_id != null) return `账本 #${event.ledger_id}`
  if (event.claim_id != null) return `声明 #${event.claim_id}`
  return event.tenant_id != null ? `租户 #${event.tenant_id}` : '平台'
}

function auditTone(severity: string): AuditTone {
  switch (severity.toLowerCase()) {
    case 'critical':
    case 'error': return 'danger'
    case 'warn':
    case 'warning': return 'warn'
    case 'info': return 'info'
    case 'success': return 'ok'
    default: return 'muted'
  }
}

function formatShortTime(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
