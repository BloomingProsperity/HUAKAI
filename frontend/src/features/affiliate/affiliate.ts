import type { BadgeTone } from '../../ui/StatusBadge'
import type { ReferralItem, RewardLedgerItem } from './types'

export interface ReferralTableRow {
  id: number
  referee: string
  statusLabel: string
  statusTone: BadgeTone
  invitedAt: string
  rewardedAt: string
}

export interface RewardTableRow {
  id: string
  referral: string
  type: string
  amount: string
  createdAt: string
}

/*
 * 推广页纯逻辑(可单测):邀请链接构造、金额格式化、状态标签/语气映射。
 * 不触网、无副作用,便于变异测试。
 */

/**
 * 由邀请码 + 站点 origin 构造完整邀请链接。
 * 形态:<origin>/register?invite=<编码后的 code>。
 * - origin 末尾斜杠会被去掉,避免出现双斜杠。
 * - code 经 encodeURIComponent 编码,防止特殊字符破坏 query。
 * - code 为空时返回空串(调用方据此隐藏复制按钮)。
 */
export function buildInviteLink(origin: string, code: string): string {
  const c = code.trim()
  if (!c) return ''
  const base = origin.replace(/\/+$/, '')
  return `${base}/register?invite=${encodeURIComponent(c)}`
}

/**
 * 把「分(cents)」格式化为「¥X.XX」展示串(两位小数,整数分四舍五入到分本身无损)。
 * 判别核心:1 分 = 0.01,故除以 100;负数与非有限值回退为「¥0.00」。
 */
export function formatCents(cents: number): string {
  if (!Number.isFinite(cents) || cents < 0) return '¥0.00'
  return `¥${(cents / 100).toFixed(2)}`
}

/**
 * 把后端 decimal 字符串金额标准化为「$X.XX」展示。
 * 后端 total_reward_usd / amount_usd 是 decimal 字符串(可能为 "0"、"1.5"、"" 等),
 * 这里解析为数字后固定两位小数;无法解析则展示「$0.00」。
 */
export function formatUsd(raw: string): string {
  const n = Number.parseFloat(raw)
  if (!Number.isFinite(n)) return '$0.00'
  return `$${n.toFixed(2)}`
}

/** 被邀请人状态 → 中文标签。未知状态原样返回。 */
export function referralStatusLabel(status: string): string {
  switch (status) {
    case 'pending':
      return '待合格'
    case 'qualified':
      return '已合格'
    case 'rewarded':
      return '已返利'
    case 'rejected':
      return '已驳回'
    default:
      return status
  }
}

/**
 * 被邀请人状态 → 徽章语气。
 * 判别核心:rewarded=ok(成功),qualified=info(进行中),pending=warn(待定),
 * rejected=danger(终态失败)。变异(把 rewarded 映射成非 ok)→ 断言 RED。
 */
export function referralStatusTone(status: string): BadgeTone {
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

/** 把被邀请人 user id 脱敏成「用户 #<id>」展示名(不暴露邮箱等 PII)。 */
export function refereeDisplay(userId: number): string {
  return `用户 #${userId}`
}

/** 被邀请人响应到列表展示行的纯映射。 */
export function mapReferralRows(items: ReferralItem[]): ReferralTableRow[] {
  return items.map((item) => ({
    id: item.referral_id,
    referee: refereeDisplay(item.referee_user_id),
    statusLabel: referralStatusLabel(item.status),
    statusTone: referralStatusTone(item.status),
    invitedAt: formatPortalTime(item.created_at),
    rewardedAt: item.rewarded_at ? formatPortalTime(item.rewarded_at) : '—',
  }))
}

/** 返利流水响应到列表展示行的纯映射，序号仅用于保持重复关联记录的行键唯一。 */
export function mapRewardRows(items: RewardLedgerItem[]): RewardTableRow[] {
  return items.map((item, index) => ({
    id: `${item.referral_id}-${index}`,
    referral: `#${item.referral_id}`,
    type: item.reward_type,
    amount: formatUsd(item.amount_usd),
    createdAt: formatPortalTime(item.created_at),
  }))
}

function formatPortalTime(iso: string): string {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}

/*
 * 活动邀请码生成的前端校验,严格镜像后端范围(避免无谓往返,真校验仍在后端):
 *   - max_usage      ∈ [1, 100]   (invitation.DefaultMaxUsage=1 / MaxUsageLimit=100)
 *   - expires_in_days∈ [1, 90]    (invitation.DefaultExpiryDays=30 / maxExpiresDays=90)
 * 后端真码:backend/internal/community/invitation/{types.go,service.go}。
 */
export const MAX_USAGE_MIN = 1
export const MAX_USAGE_MAX = 100
export const EXPIRES_DAYS_MIN = 1
export const EXPIRES_DAYS_MAX = 90
export const DEFAULT_MAX_USAGE = 1
export const DEFAULT_EXPIRES_DAYS = 30

export interface MintFormValidation {
  ok: boolean
  /** 校验失败时的中文提示;ok 时为空串。 */
  error: string
  /** 校验通过时,规范化后的整数 max_usage(ok=false 时为 0)。 */
  maxUsage: number
  /** 校验通过时,规范化后的整数 expires_in_days(ok=false 时为 0)。 */
  expiresInDays: number
}

/**
 * 校验生成活动邀请码的两个数值输入(均来自文本框,故入参为 string)。
 * 判别核心:
 *   - 必须是整数(拒小数 / 非数字 / 空);
 *   - max_usage ∈ [1,100],expires_in_days ∈ [1,90](闭区间,镜像后端);
 *   - 越界 / 非整数 → ok=false 且给出针对性中文 error。
 * 变异(放宽上界 / 接受小数 / 边界判错)→ 对应断言 RED。
 */
export function validateMintForm(maxUsageRaw: string, expiresInDaysRaw: string): MintFormValidation {
  const fail = (error: string): MintFormValidation => ({ ok: false, error, maxUsage: 0, expiresInDays: 0 })

  const maxUsage = parseStrictInt(maxUsageRaw)
  if (maxUsage === null) return fail('使用次数必须是整数')
  if (maxUsage < MAX_USAGE_MIN || maxUsage > MAX_USAGE_MAX) {
    return fail(`使用次数需在 ${MAX_USAGE_MIN}–${MAX_USAGE_MAX} 之间`)
  }

  const expiresInDays = parseStrictInt(expiresInDaysRaw)
  if (expiresInDays === null) return fail('有效天数必须是整数')
  if (expiresInDays < EXPIRES_DAYS_MIN || expiresInDays > EXPIRES_DAYS_MAX) {
    return fail(`有效天数需在 ${EXPIRES_DAYS_MIN}–${EXPIRES_DAYS_MAX} 之间`)
  }

  return { ok: true, error: '', maxUsage, expiresInDays }
}

/**
 * 严格整数解析:仅接受可选前后空白包裹的纯十进制整数串(如 "1"、" 100 ")。
 * 拒绝空串、小数("1.5")、带符号外的非数字、Infinity/NaN。失败返回 null。
 */
function parseStrictInt(raw: string): number | null {
  const s = raw.trim()
  if (!/^\d+$/.test(s)) return null
  const n = Number.parseInt(s, 10)
  return Number.isSafeInteger(n) ? n : null
}
