import type { BadgeTone } from '../../ui/StatusBadge'

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
