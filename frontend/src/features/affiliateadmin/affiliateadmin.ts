import type {
  AdminReferralItem,
  AdminReferralOverview,
  AdminReferralRewardItem,
  AffiliateFilters,
  ReferralStatus,
} from './types'

/*
 * 分销管理纯逻辑(可单测):筛选条件 → query 参数构造、状态配色/中文标签、
 * decimal 金额安全展示。全部无副作用,不碰网络/DOM,便于变异测试打红。
 */

export type QueryValue = string | number | undefined

/** 空租户筛选优先采用登录上下文；显式输入始终保留。 */
export function withTenantContext(filters: AffiliateFilters, tenantId: number | null): AffiliateFilters {
  if (filters.tenantId.trim() || tenantId == null || !Number.isInteger(tenantId) || tenantId <= 0) return filters
  return { ...filters, tenantId: String(tenantId) }
}

/**
 * 据筛选条件构造列表 query。判别核心:tenant_id 仅在非空白时下发;
 * status 仅在为合法分销状态时下发(防止把任意输入透传给后端触发 400)。
 * limit/offset 永远显式带(分页所需)。
 */
export function buildReferralQuery(
  filters: AffiliateFilters,
  limit: number,
  offset: number,
): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = { limit, offset }
  const tenant = filters.tenantId.trim()
  if (tenant) q.tenant_id = tenant
  const status = filters.status.trim()
  // 仅放行合法状态;非法/空一律省略(全部)。这是判别核心,变异成无条件赋值会让测试 RED。
  if (status && isReferralStatus(status)) q.status = status
  return q
}

/**
 * 据筛选条件构造返利账本 query。账本端点不吃 status,只吃 tenant_id + referrer_user_id。
 * 此处仅透传 tenant_id(referrer 过滤由 UI 另传)。
 */
export function buildRewardsQuery(
  filters: AffiliateFilters,
  limit: number,
  offset: number,
  referrerUserId?: string,
): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = { limit, offset }
  const tenant = filters.tenantId.trim()
  if (tenant) q.tenant_id = tenant
  const referrer = (referrerUserId ?? '').trim()
  if (referrer) q.referrer_user_id = referrer
  return q
}

/** 构造概览 query(仅 tenant_id)。 */
export function buildOverviewQuery(filters: AffiliateFilters): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = {}
  const tenant = filters.tenantId.trim()
  if (tenant) q.tenant_id = tenant
  return q
}

const VALID_STATUSES: ReadonlySet<string> = new Set<ReferralStatus>([
  'pending',
  'qualified',
  'rewarded',
  'rejected',
])

/** 是否为合法分销状态(对齐后端 invitation.ValidReferralStatus)。 */
export function isReferralStatus(s: string): s is ReferralStatus {
  return VALID_STATUSES.has(s)
}

export type StatusTone = 'ok' | 'info' | 'warn' | 'danger' | 'muted'

/**
 * 分销状态 → 徽章语气。判别核心:rewarded(已返利,落了钱)= ok 绿;
 * qualified(达标待发)= info;pending(待定)= warn;rejected(驳回)= danger。
 */
export function statusTone(status: string): StatusTone {
  switch (status) {
    case 'rewarded':
      return 'ok'
    case 'qualified':
      return 'info'
    case 'pending':
      return 'warn'
    case 'rejected':
      return 'danger'
    default:
      return 'muted'
  }
}

const STATUS_LABEL: Record<string, string> = {
  pending: '待定',
  qualified: '已达标',
  rewarded: '已返利',
  rejected: '已驳回',
}

/** 分销状态 → 中文标签。未知值原样返回(便于排查后端新增态)。 */
export function statusLabel(status: string): string {
  return STATUS_LABEL[status] ?? status
}

/**
 * decimal 字符串金额安全展示。判别核心:空/纯空白/非数字 → '0';
 * 否则原样去首尾空白返回(绝不做浮点运算,保精度)。涉及钱:只读展示。
 */
export function formatUsd(amount: string | null | undefined): string {
  const v = (amount ?? '').trim()
  if (!v) return '0'
  // 仅校验是否像十进制数;不解析成 number 以免丢精度。
  if (!/^-?\d+(\.\d+)?$/.test(v)) return '0'
  return v
}

/**
 * 从概览的 counts_by_status 安全取某状态计数(缺键 → 0)。
 * 判别核心:即便后端漏返某状态键,UI 也稳定显示 0 而非 NaN/undefined。
 */
export function statusCount(counts: Record<string, number> | null | undefined, status: ReferralStatus): number {
  if (!counts) return 0
  const v = counts[status]
  return typeof v === 'number' && Number.isFinite(v) ? v : 0
}

export interface AffiliateStatView {
  label: string
  value: string
  tone: 'default' | 'ok'
}

/** 分销概览到共享指标卡的纯映射。 */
export function mapAffiliateStats(overview: AdminReferralOverview | null): AffiliateStatView[] {
  return [
    { label: '累计返利(USD)', value: overview ? formatUsd(overview.total_reward_usd) : '—', tone: 'ok' },
    { label: '已发返利笔数', value: overview ? String(overview.reward_count) : '—', tone: 'default' },
    { label: '待定 / 已达标', value: overview ? `${statusCount(overview.counts_by_status, 'pending')} / ${statusCount(overview.counts_by_status, 'qualified')}` : '—', tone: 'default' },
    { label: '已返利 / 已驳回', value: overview ? `${statusCount(overview.counts_by_status, 'rewarded')} / ${statusCount(overview.counts_by_status, 'rejected')}` : '—', tone: 'default' },
  ]
}

export interface ReferralTableRow {
  id: number
  referrerUserId: string
  refereeUserId: string
  status: string
  createdAt: string
}

/** 分销记录到列表列的纯映射。 */
export function mapReferralTableRows(items: AdminReferralItem[]): ReferralTableRow[] {
  return items.map((item) => ({
    id: item.id,
    referrerUserId: `#${item.referrer_user_id}`,
    refereeUserId: `#${item.referee_user_id}`,
    status: item.status,
    createdAt: formatAffiliateTime(item.created_at),
  }))
}

export interface RewardTableRow {
  id: number
  referralId: string
  referrerUserId: string
  rewardType: string
  amountUsd: string
  issuedAt: string
}

/** 返利流水到列表列的纯映射，金额不转换为 number。 */
export function mapRewardTableRows(items: AdminReferralRewardItem[]): RewardTableRow[] {
  return items.map((item) => ({
    id: item.id,
    referralId: `#${item.referral_id}`,
    referrerUserId: `#${item.referrer_user_id}`,
    rewardType: item.reward_type || '—',
    amountUsd: formatUsd(item.amount_usd),
    issuedAt: formatAffiliateTime(item.issued_at),
  }))
}

function formatAffiliateTime(iso: string): string {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString('zh-CN', { hour12: false })
}
