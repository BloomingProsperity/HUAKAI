import type { PlanView, PurchaseRequest, SubscriptionProgressView, SubscriptionView } from './types'

/*
 * 订阅页纯逻辑(可单测, 不触 DOM / 不发请求)。
 * 职责:套餐价格/有效期格式化、配额上限展示、购买请求构造与前端校验、
 *      进度窗口的百分比夹取与重置倒计时格式化、窗口类型 → 中文标签、
 *      当前订阅状态 → 中文标签 + 徽章语气。
 * 金额均来自后端字符串(USD 小数)或「分」整数;前端不做浮点重算钱,只做展示。
 */

/** 货币符号表(展示用;未命中回退为「数额 币种」)。 */
const CURRENCY_SYMBOLS: Record<string, string> = {
  USD: '$',
  CNY: '¥',
  RMB: '¥',
  EUR: '€',
  GBP: '£',
}

/**
 * 分 → 货币展示串。priceCents 以「分」为单位, 除 100 取两位小数。
 * 判别核心:必须除以 100(变异成除 1 或除 1000 → 价格错位 → RED)。
 */
export function formatPrice(priceCents: number, currencyCode: string): string {
  const major = priceCents / 100
  const symbol = CURRENCY_SYMBOLS[currencyCode.toUpperCase()] ?? ''
  const text = major.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  return symbol ? `${symbol}${text}` : `${text} ${currencyCode.toUpperCase()}`
}

/** 有效期天数 → 中文。0/负数当作不限期。 */
export function formatValidity(days: number): string {
  if (days <= 0) return '不限期'
  return `${days} 天`
}

/**
 * 套餐三档配额上限的展示串(USD 小数字符串直接展示;null 表示该窗口不设上限)。
 * 返回 [日, 周, 月] 三个文案, 缺省档显示「不限」。
 */
export function formatCaps(plan: Pick<PlanView, 'daily_cap_usd' | 'weekly_cap_usd' | 'monthly_cap_usd'>): {
  daily: string
  weekly: string
  monthly: string
} {
  return {
    daily: capLabel(plan.daily_cap_usd),
    weekly: capLabel(plan.weekly_cap_usd),
    monthly: capLabel(plan.monthly_cap_usd),
  }
}

/** 单个上限字段 → 展示串:空/无 → 「不限」, 否则带 $ 前缀的 USD 串。 */
export function capLabel(capUsd?: string | null): string {
  if (capUsd == null || capUsd === '') return '不限'
  return `$${capUsd}`
}

/**
 * 构造购买请求体。
 * 判别核心:planId 必须原样落到 plan_id(变异成漏带/置 0 → 后端 invalid_plan → RED)。
 */
export function buildPurchaseRequest(planId: number): PurchaseRequest {
  return { plan_id: planId }
}

/** 前端先校验套餐是否可购:必须 enabled && for_sale, 且 id 为正。通过返回 null。 */
export function validatePurchasable(plan: Pick<PlanView, 'id' | 'enabled' | 'for_sale'>): string | null {
  if (!plan.id || plan.id <= 0) return '套餐无效'
  if (!plan.enabled) return '该套餐已停用'
  if (!plan.for_sale) return '该套餐当前不可购买'
  return null
}

/**
 * 进度百分比(展示用整数)。后端已给 usage_percent(可超 100),前端这里夹到 [0, 100]
 * 仅用于「进度条宽度」, 文案另外用原始百分比展示超额。
 * 判别核心:必须夹到不超过 100(变异成不夹 → 超额时进度条溢出 → RED);下界夹 0。
 */
export function clampBarPercent(usagePercent: number): number {
  if (!Number.isFinite(usagePercent) || usagePercent < 0) return 0
  if (usagePercent > 100) return 100
  return Math.round(usagePercent)
}

/**
 * 重置倒计时格式化。秒数 → 「Xd Yh」/「Yh Zm」/「Zm」/「即将重置」。
 * 判别核心:1 天 = 86400 秒(变异成 3600 等 → 天数错位 → RED);0/负数 → 即将重置。
 */
export function formatResetCountdown(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '即将重置'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const mins = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时后重置`
  if (hours > 0) return `${hours} 小时 ${mins} 分后重置`
  if (mins > 0) return `${mins} 分后重置`
  return '不到 1 分钟后重置'
}

/** 配额窗口类型 → 中文标签。 */
export function windowLabel(kind: string): string {
  switch (kind) {
    case 'calendar_day':
      return '当日'
    case 'calendar_week':
      return '本周'
    case 'calendar_month':
      return '本月'
    default:
      return kind
  }
}

/**
 * 进度窗口排序:日 → 周 → 月(后端返回顺序不保证),稳定展示。
 * 判别核心:未知类型排到最后(变异成排最前 → 顺序错乱 → RED)。
 */
export function sortProgressWindows(rows: SubscriptionProgressView[]): SubscriptionProgressView[] {
  const order: Record<string, number> = { calendar_day: 0, calendar_week: 1, calendar_month: 2 }
  return [...rows].sort((a, b) => {
    const ra = order[a.window_kind] ?? 99
    const rb = order[b.window_kind] ?? 99
    return ra - rb
  })
}

/** 订阅状态 → 中文标签。 */
export function subscriptionStatusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '生效中'
    case 'expired':
      return '已过期'
    case 'cancelled':
    case 'canceled':
      return '已取消'
    case 'pending':
      return '待生效'
    default:
      return status || '未知'
  }
}

/** 订阅状态 → 徽章语气键(由页面映射到 StatusBadge tone)。 */
export type SubTone = 'ok' | 'warn' | 'danger' | 'muted'

export function subscriptionStatusTone(status: string): SubTone {
  switch (status) {
    case 'active':
      return 'ok'
    case 'pending':
      return 'warn'
    case 'expired':
    case 'cancelled':
    case 'canceled':
      return 'danger'
    default:
      return 'muted'
  }
}

/** 时间字符串 → 本地化展示(无效时间回退空串)。 */
export function formatDate(iso?: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString('zh-CN')
}

/**
 * 是否处于超额(后端 over_limit 优先;无则用 usage_percent>100 兜底)。
 * 判别核心:over_limit 为 true 时即超额, 与百分比无关(变异成只看百分比 → 边界漏判 → RED)。
 */
export function isOverLimit(row: Pick<SubscriptionProgressView, 'over_limit' | 'usage_percent'>): boolean {
  return row.over_limit || row.usage_percent > 100
}

/**
 * 购买后引导文案。idempotent=true 说明已有同款待支付订单, 不再重复建单。
 */
export function purchaseGuidance(outTradeNo: string, idempotent: boolean): string {
  const base = `订单已创建(单号 ${outTradeNo}),请前往支付完成购买;支付确认后订阅自动生效。`
  return idempotent ? `已有未支付的同款订单(单号 ${outTradeNo}),请直接前往支付即可,无需重复下单。` : base
}

/** 当前订阅是否仍在生效期内(用于页面判断展示「续订/购买」入口)。 */
export function isSubscriptionActive(sub: SubscriptionView | null): boolean {
  if (!sub) return false
  return sub.status === 'active'
}

/**
 * 订阅历史排序:按创建时间倒序(最新的订阅记录排最前),便于用户最先看到最近一次订阅。
 * 后端 ListUserSubscriptions 返回顺序不保证,故前端稳定排序。
 * 判别核心:必须倒序(最新在前)。变异成正序(最旧在前)→ 顺序反 → RED;
 *          无效/缺失 created_at 当作最早(排最后),不抛错。
 */
export function sortSubscriptionHistory(rows: SubscriptionView[]): SubscriptionView[] {
  const ts = (s: SubscriptionView): number => {
    const t = new Date(s.created_at).getTime()
    return Number.isNaN(t) ? -Infinity : t
  }
  return [...rows].sort((a, b) => ts(b) - ts(a))
}

export interface SubscriptionHistoryTableRow {
  id: number
  planId: string
  status: string
  tone: SubTone
  group: string
  startsAt: string
  expiresAt: string
  cancelledAt: string
  createdAt: string
}

/** 订阅历史到七列表格的纯映射；空字段统一展示为破折号。 */
export function mapSubscriptionHistoryRows(rows: SubscriptionView[]): SubscriptionHistoryTableRow[] {
  return sortSubscriptionHistory(rows).map((row) => ({
    id: row.id,
    planId: String(row.plan_id),
    status: subscriptionStatusLabel(row.status),
    tone: subscriptionStatusTone(row.status),
    group: row.granted_group || '—',
    startsAt: formatDate(row.starts_at) || '—',
    expiresAt: formatDate(row.expires_at) || '—',
    cancelledAt: formatDate(row.cancelled_at) || '—',
    createdAt: formatDate(row.created_at) || '—',
  }))
}

/**
 * 自助换套餐的目标套餐候选:从在售套餐里剔除「当前订阅所属套餐」(换成自己无意义,后端也会拒),
 * 且只保留可购(enabled && for_sale)的套餐。
 * 判别核心:必须排除 currentPlanId(变异成不排除 → 出现「换成当前套餐」选项 → RED);
 *          必须过滤不可售(变异成放行 → 列出不可换的套餐 → RED)。
 */
export function changeablePlans(
  plans: Pick<PlanView, 'id' | 'enabled' | 'for_sale'>[],
  currentPlanId: number | null | undefined,
): Pick<PlanView, 'id' | 'enabled' | 'for_sale'>[] {
  return plans.filter((p) => p.id !== currentPlanId && p.enabled && p.for_sale)
}

/**
 * 换套餐前端校验:目标 plan id 必须为正,且不能与当前套餐相同(同档换无意义)。
 * 通过返回 null,否则返回中文错误文案。
 * 判别核心:newPlanId === currentPlanId 必须拦下(变异成放行 → 发无效换套餐请求 → RED)。
 */
export function validateChangePlan(newPlanId: number, currentPlanId: number | null | undefined): string | null {
  if (!newPlanId || newPlanId <= 0) return '请选择要换的目标套餐'
  if (currentPlanId != null && newPlanId === currentPlanId) return '已是当前套餐,无需更换'
  return null
}

/**
 * 关闭自动续订后的提示文案。强调「当前权益不受影响,到期不再续费」,避免用户误以为立刻失效。
 * 判别核心:必须含「到期」语义且不能说「立即取消/失效」(变异成误导文案 → RED)。
 */
export function cancelRenewGuidance(expiresAt?: string | null): string {
  const tail = expiresAt ? `,当前订阅将保留至 ${formatDate(expiresAt)} 到期。` : ',当前订阅在到期前仍可正常使用。'
  return `自动续订已关闭,到期后将不再自动续费${tail}`
}

/** 换套餐路径后端错误码 → 友好中文(对照 subscriptionhttp writeSubscriptionError / 校验分支)。 */
export function friendlyChangePlanError(code: string, fallback?: string): string {
  switch (code) {
    case 'invalid_plan':
      return '目标套餐无效,请重新选择'
    case 'plan_not_for_sale':
      return '目标套餐当前不可购买'
    case 'subscription_not_found':
    case 'no_active_subscription':
      return '当前没有可更换的生效订阅'
    case 'downgrade_not_allowed':
      return '降级需联系管理员处理,自助仅支持升级'
    case 'session_token_required':
      return '登录状态已失效,请重新登录后再操作'
    case 'gateway_not_configured':
    case 'subscription_backend_error':
      return '订阅服务暂时不可用,请稍后再试'
    default:
      return fallback || '换套餐失败,请稍后再试'
  }
}
