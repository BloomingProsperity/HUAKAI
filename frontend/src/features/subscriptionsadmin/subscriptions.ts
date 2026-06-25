import type { BadgeTone } from '../../ui/StatusBadge'
import type { Plan, PlanFormState, UpsertPlanRequest } from './types'

/*
 * 套餐管理纯逻辑(可单测):金额分↔美元换算、表单→请求体归一与校验、订阅状态配色。
 * 不触碰 DOM / fetch / store。每个导出函数都被 subscriptions.test.ts 覆盖,
 * 至少一条断言可被变异打红(注释标判别核心)。
 *
 * secret-mask:本模块不处理任何密钥/明文,无 console。
 */

/** 表单校验结果:ok 时 request 为可下发请求体;否则 error 为中文文案。 */
export type ValidationResult =
  | { ok: true; request: UpsertPlanRequest }
  | { ok: false; error: string }

/** 美元字符串 → 分(整数)。空串视为 0。非法/负数返回 null(调用方据此报错)。 */
export function usdToCents(usd: string): number | null {
  const v = usd.trim()
  if (v === '') return 0
  // 仅允许非负十进制(可带小数);避免把 '1e3' / '0x10' 这类放过。
  if (!/^\d+(\.\d+)?$/.test(v)) return null
  const num = Number(v)
  if (!Number.isFinite(num) || num < 0) return null
  // 四舍五入到分,规避浮点尾差(如 19.99 * 100 = 1998.9999…)。
  return Math.round(num * 100)
}

/** 分 → 美元展示字符串(两位小数)。用于列表与编辑回填。 */
export function centsToUsd(cents: number): string {
  if (!Number.isFinite(cents)) return '0.00'
  return (cents / 100).toFixed(2)
}

/** 归一封顶输入:空串→省略(undefined);否则 trim。非负十进制校验留给后端(parseCap)。 */
function normCap(raw: string): string | undefined {
  const v = raw.trim()
  return v === '' ? undefined : v
}

/**
 * 表单态 → 建/改套餐请求体并做提交前校验。
 * 判别核心:name 必填、validity_days 必须为正整数、price 必须合法非负;cap 空串省略不下发。
 */
export function buildPlanRequest(form: PlanFormState, tenantID: number): ValidationResult {
  const name = form.name.trim()
  if (name === '') return { ok: false, error: '套餐名称必填' }

  const days = Number(form.validityDays.trim())
  if (!Number.isInteger(days) || days <= 0) {
    return { ok: false, error: '有效天数必须为正整数' }
  }

  const cents = usdToCents(form.priceUsd)
  if (cents === null) return { ok: false, error: '价格必须为非负数字' }

  const sortRaw = form.sortOrder.trim()
  let sortOrder = 0
  if (sortRaw !== '') {
    const s = Number(sortRaw)
    if (!Number.isInteger(s)) return { ok: false, error: '排序值必须为整数' }
    sortOrder = s
  }

  const req: UpsertPlanRequest = {
    tenant_id: tenantID,
    name,
    validity_days: days,
    price_cents: cents,
    for_sale: form.forSale,
    sort_order: sortOrder,
  }
  const desc = form.description.trim()
  if (desc !== '') req.description = desc
  const currency = form.currencyCode.trim()
  if (currency !== '') req.currency_code = currency
  const group = form.grantedGroup.trim()
  if (group !== '') req.granted_group = group
  const daily = normCap(form.dailyCapUsd)
  if (daily !== undefined) req.daily_cap_usd = daily
  const weekly = normCap(form.weeklyCapUsd)
  if (weekly !== undefined) req.weekly_cap_usd = weekly
  const monthly = normCap(form.monthlyCapUsd)
  if (monthly !== undefined) req.monthly_cap_usd = monthly

  return { ok: true, request: req }
}

/** 套餐 → 编辑表单回填态(分→美元、null cap→空串)。 */
export function planToForm(p: Plan): PlanFormState {
  const cap = (v: string | null | undefined): string => (v == null ? '' : v)
  return {
    name: p.name,
    description: p.description ?? '',
    priceUsd: centsToUsd(p.price_cents),
    currencyCode: p.currency_code || 'USD',
    validityDays: String(p.validity_days),
    grantedGroup: p.granted_group ?? '',
    dailyCapUsd: cap(p.daily_cap_usd),
    weeklyCapUsd: cap(p.weekly_cap_usd),
    monthlyCapUsd: cap(p.monthly_cap_usd),
    forSale: p.for_sale,
    sortOrder: String(p.sort_order),
  }
}

/** 订阅状态 → 徽章语气。active 绿,pending/grace 警告,cancelled/expired/revoked 危险。 */
export function subscriptionTone(status: string): BadgeTone {
  switch (status.toLowerCase()) {
    case 'active':
      return 'ok'
    case 'pending':
    case 'grace':
    case 'paused':
      return 'warn'
    case 'cancelled':
    case 'canceled':
    case 'expired':
    case 'revoked':
      return 'danger'
    default:
      return 'muted'
  }
}

/** 套餐上架/启用态 → 徽章语气。停用优先(danger),其次按是否上架。 */
export function planTone(plan: Plan): BadgeTone {
  if (!plan.enabled) return 'danger'
  return plan.for_sale ? 'ok' : 'muted'
}

/** 套餐上架/启用态 → 中文标签。 */
export function planStatusLabel(plan: Plan): string {
  if (!plan.enabled) return '已停用'
  return plan.for_sale ? '在售' : '未上架'
}
