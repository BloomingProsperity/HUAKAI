import type { BadgeTone } from '../../ui/StatusBadge'
import type {
  AdminSubscription,
  CreateSubscriptionVoucherRequest,
  ExtendAssignmentRequest,
  Plan,
  PlanFormState,
  UpsertPlanRequest,
} from './types'

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

export interface PlanStatView {
  label: string
  value: string
  hint: string
  tone: 'default' | 'danger' | 'warn' | 'ok'
}

/** 当前套餐列表到三张统计卡的纯映射，未加载不冒充为零。 */
export function mapPlanStats(plans: Plan[] | null): PlanStatView[] {
  if (plans === null) {
    return [
      { label: '套餐总数', value: '—', hint: '当前页数据加载中', tone: 'default' },
      { label: '启用', value: '—', hint: '当前页数据加载中', tone: 'ok' },
      { label: '停用', value: '—', hint: '当前页数据加载中', tone: 'danger' },
    ]
  }
  const enabled = plans.filter((plan) => plan.enabled).length
  const disabled = plans.length - enabled
  return [
    { label: '套餐总数', value: `${plans.length.toLocaleString('zh-CN')} 个`, hint: '当前页口径', tone: 'default' },
    { label: '启用', value: `${enabled.toLocaleString('zh-CN')} 个`, hint: '当前页口径', tone: 'ok' },
    { label: '停用', value: `${disabled.toLocaleString('zh-CN')} 个`, hint: '当前页口径', tone: 'danger' },
  ]
}

export interface PlanTableRow {
  id: number
  source: Plan
  name: string
  description: string
  price: string
  validity: string
  caps: string
  group: string
  statusText: string
  statusTone: BadgeTone
}

/** 后端套餐项到列表行的纯映射；金额仅做分到主货币单位的展示换算。 */
export function mapPlanRows(plans: Plan[]): PlanTableRow[] {
  return plans.map((plan) => ({
    id: plan.id,
    source: plan,
    name: plan.name,
    description: plan.description ?? '',
    price: `${centsToUsd(plan.price_cents)} ${plan.currency_code}`,
    validity: `${plan.validity_days} 天`,
    caps: formatCaps(plan.daily_cap_usd, plan.weekly_cap_usd, plan.monthly_cap_usd),
    group: plan.granted_group || '—',
    statusText: planStatusLabel(plan),
    statusTone: planTone(plan),
  }))
}

export interface AssignmentTableRow {
  id: number
  source: AdminSubscription
  subscriptionID: string
  planID: string
  status: string
  statusTone: BadgeTone
  startsAt: string
  expiresAt: string
}

/** 后端用户订阅项到分配子表行的纯映射，保留 source 供行内动作使用。 */
export function mapAssignmentRows(subscriptions: AdminSubscription[]): AssignmentTableRow[] {
  return subscriptions.map((subscription) => ({
    id: subscription.id,
    source: subscription,
    subscriptionID: `#${subscription.id}`,
    planID: String(subscription.plan_id),
    status: subscription.status,
    statusTone: subscriptionTone(subscription.status),
    startsAt: formatAdminDate(subscription.starts_at),
    expiresAt: formatAdminDate(subscription.expires_at),
  }))
}

/** 套餐与订阅共用的日/周/月封顶展示，空值代表不限。 */
export function formatCaps(
  daily: string | null | undefined,
  weekly: string | null | undefined,
  monthly: string | null | undefined,
): string {
  return `${daily ?? '∞'} / ${weekly ?? '∞'} / ${monthly ?? '∞'}`
}

/** 运维列表与详情共用的日期展示；非法原值不丢失，便于排查数据问题。 */
export function formatAdminDate(iso: string): string {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleDateString('zh-CN')
}

/* ---- 批量分配 / 延长 / 撤销 / 兑换券 的纯逻辑校验 ---- */

/**
 * 解析批量用户 ID 文本(逗号/空格/换行分隔)→ 去重正整数数组。
 * 判别核心:每个 token 必须是正整数,出现非法即整体失败(避免静默吞掉错误 ID 误分配)。
 */
export function parseBulkUserIDs(raw: string): { ids: number[] } | { error: string } {
  const tokens = raw
    .split(/[\s,]+/)
    .map((t) => t.trim())
    .filter((t) => t !== '')
  if (tokens.length === 0) return { error: '请填写至少一个用户 ID' }
  const ids: number[] = []
  const seen = new Set<number>()
  for (const t of tokens) {
    if (!/^\d+$/.test(t)) return { error: `用户 ID「${t}」非法(必须为正整数)` }
    const n = Number(t)
    if (!Number.isInteger(n) || n <= 0) return { error: `用户 ID「${t}」非法(必须为正整数)` }
    if (!seen.has(n)) {
      seen.add(n)
      ids.push(n)
    }
  }
  return { ids }
}

/** 延长方式:按天数(days)或按到期时间(until)。 */
export type ExtendMode = 'days' | 'until'

/**
 * 构造延长请求体。判别核心:
 *  - days 模式:days 必须为正整数;
 *  - until 模式:until 必须是可解析且未来(> now)的时间。
 * 两者互斥,仅下发选中的那个字段(对齐后端 omitempty,避免同时传 days+until 语义歧义)。
 */
export function buildExtendRequest(
  mode: ExtendMode,
  tenantID: number,
  daysRaw: string,
  untilRaw: string,
  now: number = Date.now(),
): { request: ExtendAssignmentRequest } | { error: string } {
  if (mode === 'days') {
    const d = Number(daysRaw.trim())
    if (!Number.isInteger(d) || d <= 0) return { error: '延长天数必须为正整数' }
    return { request: { tenant_id: tenantID, days: d } }
  }
  const v = untilRaw.trim()
  if (v === '') return { error: '请填写到期时间' }
  const ts = Date.parse(v)
  if (Number.isNaN(ts)) return { error: '到期时间格式非法' }
  if (ts <= now) return { error: '到期时间必须晚于当前时间' }
  return { request: { tenant_id: tenantID, until: new Date(ts).toISOString() } }
}

/* ---- 单条分配详情:审计事件展示的纯逻辑(只读) ---- */

/**
 * 审计事件类型 → 中文标签。字面值对齐后端 internal/subscription/types.go:176 起的
 * Audit* 常量(subscription_created 等)。未知类型回退原始串(不吞掉,便于排查新事件)。
 * 判别核心:已知键必须映射到对应中文,而非恒等返回原串。
 */
export function auditEventLabel(eventType: string): string {
  switch (eventType) {
    case 'subscription_created':
      return '订阅创建'
    case 'subscription_renewed':
      return '订阅续期'
    case 'subscription_plan_updated':
      return '套餐变更'
    case 'subscription_extended':
      return '有效期延长'
    case 'subscription_quota_reset':
      return '配额重置'
    case 'subscription_revoked':
      return '订阅撤销'
    case 'expired':
      return '已过期'
    case 'cancelled':
      return '已取消'
    case 'group_upgraded':
      return '用户组升级'
    case 'group_downgraded':
      return '用户组降级'
    case 'idempotent_replay':
      return '幂等重放'
    default:
      return eventType
  }
}

/**
 * 操作者展示串:actor_kind(admin/user/system)→ 中文,带 actor_id(>0 才拼接)。
 * 字面值对齐后端 types.go:211 ActorKind* 常量。
 * 判别核心:① 已知 kind 映射为中文;② actor_id 为正才追加「#id」,0/缺省不拼
 *(系统事件常无 actor_id,拼「#0」是误导)。
 */
export function actorLabel(actorKind: string, actorID?: number): string {
  let kind: string
  switch (actorKind) {
    case 'admin':
      kind = '管理员'
      break
    case 'user':
      kind = '用户'
      break
    case 'system':
      kind = '系统'
      break
    default:
      kind = actorKind || '—'
  }
  if (typeof actorID === 'number' && actorID > 0) {
    return `${kind} #${actorID}`
  }
  return kind
}

/** 撤销原因表单态。 */
export interface VoucherFormState {
  planId: string
  code: string
  amountUsd: string
  currencyCode: string
  validFrom: string
  validUntil: string
  maxRedemptions: string
  singleUsePerUser: boolean
  eligibleUserId: string
}

export const EMPTY_VOUCHER_FORM: VoucherFormState = {
  planId: '',
  code: '',
  amountUsd: '',
  currencyCode: 'USD',
  validFrom: '',
  validUntil: '',
  maxRedemptions: '',
  singleUsePerUser: true,
  eligibleUserId: '',
}

/**
 * 构造建券请求体并校验。判别核心:
 *  - plan_id 必须为正整数;
 *  - valid_from / valid_until 必须可解析,且 until > from;
 *  - amount_cents 由名义价美元换算(可为 0,信息性);
 *  - max_redemptions / eligible_user_id 填了才下发,且必须为正整数。
 */
export function buildVoucherRequest(
  form: VoucherFormState,
  tenantID: number,
): { request: CreateSubscriptionVoucherRequest } | { error: string } {
  const planId = Number(form.planId.trim())
  if (!Number.isInteger(planId) || planId <= 0) return { error: '套餐 ID 必须为正整数' }

  const cents = usdToCents(form.amountUsd)
  if (cents === null) return { error: '名义价必须为非负数字' }

  const fromTs = Date.parse(form.validFrom.trim())
  if (Number.isNaN(fromTs)) return { error: '生效时间格式非法' }
  const untilTs = Date.parse(form.validUntil.trim())
  if (Number.isNaN(untilTs)) return { error: '失效时间格式非法' }
  if (untilTs <= fromTs) return { error: '失效时间必须晚于生效时间' }

  const req: CreateSubscriptionVoucherRequest = {
    tenant_id: tenantID,
    plan_id: planId,
    amount_cents: cents,
    valid_from: new Date(fromTs).toISOString(),
    valid_until: new Date(untilTs).toISOString(),
    single_use_per_user: form.singleUsePerUser,
  }
  const code = form.code.trim()
  if (code !== '') req.code = code
  const currency = form.currencyCode.trim()
  if (currency !== '') req.currency_code = currency

  const maxRaw = form.maxRedemptions.trim()
  if (maxRaw !== '') {
    const m = Number(maxRaw)
    if (!Number.isInteger(m) || m <= 0) return { error: '最大兑换次数必须为正整数' }
    req.max_redemptions = m
  }
  const eligRaw = form.eligibleUserId.trim()
  if (eligRaw !== '') {
    const u = Number(eligRaw)
    if (!Number.isInteger(u) || u <= 0) return { error: '限定用户 ID 必须为正整数' }
    req.eligible_user_id = u
  }
  return { request: req }
}
