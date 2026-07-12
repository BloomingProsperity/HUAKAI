/*
 * 订单管理台纯逻辑(可单测,无 IO 无 DOM)。
 *   - 订单状态机:状态枚举/中文标签/徽章语气;
 *   - 列表筛选器 → query 构造(空字段省略,money 不掺和);
 *   - 卡单动作可用性裁定:按订单当前状态决定「确认 / 取消 / 重试」三动作是否可点。
 *
 * 状态枚举与后端真码严格对齐(payment/types.go:29 StatusPending..StatusFailed;
 * admin_panel.go:279 validOrderStatus 列出可作筛选值的 8 个状态)。
 */
import type { BadgeTone } from '../../ui/StatusBadge'

/** 后端 validOrderStatus 接受的 8 个订单状态(可作列表筛选值)。 */
export const ORDER_STATUSES: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'pending', label: '待支付' },
  { value: 'paid', label: '已支付' },
  { value: 'recharging', label: '充值中' },
  { value: 'completed', label: '已完成' },
  { value: 'refunded', label: '已退款' },
  { value: 'expired', label: '已过期' },
  { value: 'cancelled', label: '已取消' },
  { value: 'failed', label: '失败' },
]

export function statusLabel(status: string): string {
  return ORDER_STATUSES.find((s) => s.value === status)?.label ?? status
}

/** 状态 → 徽章语气。完成=ok,中间态/退款=info/warn,终止失败=danger。 */
export function statusTone(status: string): BadgeTone {
  switch (status) {
    case 'completed':
      return 'ok'
    case 'paid':
    case 'recharging':
      return 'info'
    case 'pending':
      return 'warn'
    case 'failed':
      return 'danger'
    case 'refunded':
    case 'expired':
    case 'cancelled':
      return 'muted'
    default:
      return 'muted'
  }
}

/** 列表筛选草稿(全字符串,便于直接绑定表单输入)。 */
export interface OrderFilterForm {
  tenantId: string
  userId: string
  status: string
  createdFrom: string
  createdTo: string
}

export const EMPTY_ORDER_FILTER: OrderFilterForm = {
  tenantId: '',
  userId: '',
  status: '',
  createdFrom: '',
  createdTo: '',
}

/** datetime-local 输入值(无时区)转 RFC3339(后端 parseOptionalTimeQuery 要求 RFC3339)。 */
export function toRfc3339(local: string): string {
  const trimmed = local.trim()
  if (!trimmed) return ''
  const d = new Date(trimmed)
  if (Number.isNaN(d.getTime())) return ''
  return d.toISOString()
}

/** 列表 query 字段类型(与 api.ts listOrders 入参一致)。 */
export type OrderListQuery = Record<string, string | number | undefined>

/** buildOrderListQuery 结果:成功带 query,失败带 error(判别联合,便于 UI narrow)。 */
export type BuildOrderListQueryResult = { error: string } | { query: OrderListQuery }

/**
 * 把筛选草稿构造成列表 query。tenant_id 是后端硬性必填(parsePositiveQuery),
 * 缺失/非正 → 返回 { error } 让 UI 先拦,避免发出注定 400 的请求。
 * 其余字段为空一律省略(undefined),不污染 query 串。
 */
export function buildOrderListQuery(
  form: OrderFilterForm,
  limit: number,
  offset: number,
): BuildOrderListQueryResult {
  const tenantId = Number(form.tenantId.trim())
  if (!form.tenantId.trim() || !Number.isInteger(tenantId) || tenantId <= 0) {
    return { error: '请填写有效的租户 ID(正整数)' }
  }
  const query: OrderListQuery = {
    tenant_id: tenantId,
    limit,
    offset,
  }
  const userIdRaw = form.userId.trim()
  if (userIdRaw) {
    const userId = Number(userIdRaw)
    if (!Number.isInteger(userId) || userId <= 0) return { error: '用户 ID 必须为正整数' }
    query.user_id = userId
  }
  const status = form.status.trim()
  if (status) query.status = status
  const from = toRfc3339(form.createdFrom)
  if (from) query.created_from = from
  const to = toRfc3339(form.createdTo)
  if (to) query.created_to = to
  return { query }
}

/**
 * 卡单动作可用性裁定(状态机驱动)。仅接已存在的 admin 端点:
 *   - confirm:把待支付/已支付订单确认为「已支付并履约」。仅 pending/paid 可确认。
 *   - cancel :运营撤单,仅 pending 可取消(取消的是尚未支付的挂单)。
 *   - retry  :履约卡死后重试,仅 paid/recharging 这类「已收款但未完成履约」可重试。
 * 终止态(completed/refunded/expired/cancelled/failed)一律不可动作。
 */
export interface OrderActionAvailability {
  canConfirm: boolean
  canCancel: boolean
  canRetry: boolean
}

export function orderActions(status: string): OrderActionAvailability {
  return {
    canConfirm: status === 'pending' || status === 'paid',
    canCancel: status === 'pending',
    canRetry: status === 'paid' || status === 'recharging',
  }
}

/** 是否存在任一可用动作(决定操作列是否渲染按钮)。 */
export function hasAnyAction(status: string): boolean {
  const a = orderActions(status)
  return a.canConfirm || a.canCancel || a.canRetry
}

/** 金额(cents)→ 展示串。纯整数除法避免浮点误差,保留两位。 */
export function formatCents(cents: number, currency: string): string {
  const sign = cents < 0 ? '-' : ''
  const abs = Math.abs(cents)
  const whole = Math.floor(abs / 100)
  const frac = (abs % 100).toString().padStart(2, '0')
  return `${sign}${whole}.${frac} ${currency || ''}`.trim()
}

// ── 退款(money 敏感)纯逻辑 ────────────────────────────────────────────────

/**
 * 退款表单解析结果(判别联合)。amountCents 恒为正整数分。
 * 注意:后端 RefundOrder 要求 amount_cents ∈ (0, 订单原额]
 * (store_postgres_refund.go:64 / store_memory.go:203:`<=0 || >credit.AmountCents` 即 ErrInvalidAmount),
 * **没有** amount_cents=0 当全额退的兜底。故「全额退款」由前端用订单原额(maxCents)显式算出,绝不发 0。
 */
export type RefundAmountResult = { error: string } | { amountCents: number }

/**
 * 解析退款金额输入(展示单位「元」字符串 → cents)。
 * 空串=全额退:返回订单原额 maxCents(>0);若 maxCents 未知(<=0)则报错让运营显式填,绝不发 0。
 * 非空时必须是正数且不超过订单已支付金额(maxCents)。用整数分运算避免浮点误差(0.1+0.2 陷阱)。
 */
export function parseRefundAmount(input: string, maxCents: number): RefundAmountResult {
  const trimmed = input.trim()
  if (trimmed === '') {
    // 空=全额退:用订单原额。后端无 0→原额兜底,前端必须算出正数金额。
    if (maxCents > 0) return { amountCents: maxCents }
    return { error: '无法确定订单金额,请显式填写退款金额' }
  }
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) {
    return { error: '退款金额必须是非负数,最多两位小数' }
  }
  const [whole, frac = ''] = trimmed.split('.')
  const cents = Number(whole) * 100 + Number(frac.padEnd(2, '0'))
  if (cents <= 0) return { error: '退款金额必须大于 0(留空表示全额退款)' }
  if (maxCents > 0 && cents > maxCents) {
    return { error: '退款金额不能超过订单金额' }
  }
  return { amountCents: cents }
}

/** 订单当前状态是否「可退款」。后端仅 completed(已到账充值单)可退;其余返回 order_not_refundable。 */
export function canRefund(status: string): boolean {
  return status === 'completed'
}

// ── 退款工单(refund request)状态 ─────────────────────────────────────────

export const REFUND_REQUEST_STATUSES: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'pending', label: '待审批' },
  { value: 'approved', label: '已通过' },
  { value: 'rejected', label: '已驳回' },
]

export function refundRequestStatusLabel(status: string): string {
  return REFUND_REQUEST_STATUSES.find((s) => s.value === status)?.label ?? status
}

export function refundRequestStatusTone(status: string): BadgeTone {
  switch (status) {
    case 'approved':
      return 'ok'
    case 'pending':
      return 'warn'
    case 'rejected':
      return 'danger'
    default:
      return 'muted'
  }
}

// ── CSV 导出时间窗 ────────────────────────────────────────────────────────

/** 后端 exporthttp maxExportWindow = 366 天(export.go:27)。前端先拦超窗避免 400。 */
export const EXPORT_MAX_WINDOW_DAYS = 366

/** buildExportRange 结果:成功带 from/to(RFC3339),失败带 error。 */
export type BuildExportRangeResult = { error: string } | { from: string; to: string }

/**
 * 校验并构造 CSV 导出时间窗。后端 from/to 均为【必填】RFC3339(export.go:243 parseExportRange),
 * from 必须 ≤ to,且跨度 ≤ 366 天。注意:导出端点的租户来自 admin 凭据 ScopeTenantID,
 * 不接受 tenant_id query 参(export.go:218 resolveTenantScope),故此处不处理租户。
 */
export function buildExportRange(fromLocal: string, toLocal: string): BuildExportRangeResult {
  const from = toRfc3339(fromLocal)
  if (!from) return { error: '请选择有效的导出起始时间' }
  const to = toRfc3339(toLocal)
  if (!to) return { error: '请选择有效的导出截止时间' }
  const fromMs = Date.parse(from)
  const toMs = Date.parse(to)
  if (fromMs > toMs) return { error: '起始时间不能晚于截止时间' }
  // 判别核心:跨度超 366 天必须先拦(后端 date_range_too_large)。
  if (toMs - fromMs > EXPORT_MAX_WINDOW_DAYS * 24 * 60 * 60 * 1000) {
    return { error: `导出时间窗不能超过 ${EXPORT_MAX_WINDOW_DAYS} 天` }
  }
  return { from, to }
}

/** 导出时间窗草稿。 */
export interface ExportRangeForm {
  from: string
  to: string
}

/** 默认导出窗:近 30 天(到 datetime-local 形态)。 */
export function defaultExportRange(now: Date): ExportRangeForm {
  const pad = (n: number) => n.toString().padStart(2, '0')
  const fmt = (d: Date) =>
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
  const from = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000)
  return { from: fmt(from), to: fmt(now) }
}

// ── 支付商配置(provider config)纯逻辑 ──────────────────────────────────────

/** 后端 providerKindFromPath 放行的两个支付商(admin_panel.go:291)。 */
export const PROVIDER_KINDS: ReadonlyArray<{ value: 'manual' | 'taobao'; label: string }> = [
  { value: 'manual', label: '手动充值(manual)' },
  { value: 'taobao', label: '淘宝/闲鱼(taobao)' },
]

export function providerKindLabel(kind: string): string {
  return PROVIDER_KINDS.find((p) => p.value === kind)?.label ?? kind
}

/** 支付商配置编辑草稿(全字符串/布尔,便于绑表单)。 */
export interface ProviderConfigForm {
  enabled: boolean
  checkoutUrl: string
}

/**
 * 校验支付商配置草稿(PUT 前先拦)。后端 enabled 必填(handler 强制);checkout_url 可空。
 * 当 checkout_url 非空时,前端先做轻量 URL 形态校验(必须 http(s)://),避免存进无意义串;
 * 后端本身不校验 URL 形态,这是前端体验护栏(空串=不设跳转链接,合法)。
 */
export type BuildProviderConfigResult = { error: string } | { enabled: boolean; checkoutUrl: string }

export function buildProviderConfig(form: ProviderConfigForm): BuildProviderConfigResult {
  const url = form.checkoutUrl.trim()
  if (url && !/^https?:\/\/\S+$/i.test(url)) {
    return { error: '跳转链接必须是 http(s):// 开头的有效 URL(留空表示不设链接)' }
  }
  return { enabled: form.enabled, checkoutUrl: url }
}

// ── 代客建单(money 敏感)纯逻辑 ─────────────────────────────────────────────

/**
 * 代客建单草稿。仅支持充值单(topup):订阅单(subscription)需套餐快照 + postgres store
 * (service.go:101/108),且金额来自套餐而非运营输入,属订阅子系统范畴,代客建单不覆盖,
 * 避免运营在此误以为能自定义订阅金额。
 */
export interface CreateOrderForm {
  tenantId: string
  userId: string
  /** 金额(美元,展示单位),内部转 cents。 */
  amount: string
  /** 支付渠道,默认 manual(后端缺省也是 manual,providerKindOrDefault)。 */
  providerKind: 'manual' | 'taobao'
}

export const EMPTY_CREATE_ORDER_FORM: CreateOrderForm = {
  tenantId: '',
  userId: '',
  amount: '',
  providerKind: 'manual',
}

/** 后端账本可表示上限(service.go:123 maxAmountCents = 100_000_000_000 分)。前端先拦防溢出卡单。 */
export const MAX_AMOUNT_CENTS = 100_000_000_000

/** 代客建单解析结果(判别联合)。成功带可直接发的请求字段(amountCents 为正整数分)。 */
export type BuildCreateOrderResult =
  | { error: string }
  | {
      tenantId: number
      userId: number
      amountCents: number
      outTradeNo: string
      providerKind: 'manual' | 'taobao'
    }

/**
 * 元字符串 → 正整数分。用整数运算避免浮点误差(0.1+0.2 陷阱);非法/非正/超上限均报错。
 * 与后端 service.go:120-125 对齐:amount_cents 必须 >0 且 ≤ maxAmountCents。
 */
export function parseAmountToCents(input: string): { error: string } | { cents: number } {
  const trimmed = input.trim()
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) {
    return { error: '金额必须是非负数,最多两位小数' }
  }
  const [whole, frac = ''] = trimmed.split('.')
  const cents = Number(whole) * 100 + Number(frac.padEnd(2, '0'))
  if (cents <= 0) return { error: '金额必须大于 0' }
  if (cents > MAX_AMOUNT_CENTS) return { error: '金额超出账本可表示上限' }
  return { cents }
}

/**
 * 生成稳定的 out_trade_no(后端硬性必填且需稳定,service.go:88;字符集仅 [A-Za-z0-9_-],
 * idempotency.go:9 validateOutTradeNo)。把建单意图(租户/用户/金额/时间戳)编进单号,
 * 同一意图复用同一单号即可幂等(后端按 out_trade_no 去重防双账)。
 * suffix 通常传时间戳或随机片段,保证不同建单意图不撞号。
 */
export function buildOutTradeNo(tenantId: number, userId: number, amountCents: number, suffix: string): string {
  const safeSuffix = suffix.replace(/[^A-Za-z0-9_-]/g, '')
  return `admin-t${tenantId}-u${userId}-${amountCents}-${safeSuffix}`
}

/**
 * 把代客建单草稿构造成请求字段。逐项对齐后端硬约束先拦,避免发出注定 400 的请求:
 *   - tenant_id / user_id 必须正整数(service.go:79);
 *   - amount 走 parseAmountToCents(>0 且 ≤ 上限);
 *   - out_trade_no 自动生成(稳定 + 合法字符集);
 *   - currency 固定 USD(账本仅 USD,service.go:351),不在 UI 暴露币种选择以免误填被拒。
 * nowMs 由调用方注入(便于测试确定性);它进 out_trade_no 作为去重 suffix。
 */
export function buildCreateOrderRequest(form: CreateOrderForm, nowMs: number): BuildCreateOrderResult {
  const tenantId = Number(form.tenantId.trim())
  if (!form.tenantId.trim() || !Number.isInteger(tenantId) || tenantId <= 0) {
    return { error: '请填写有效的租户 ID(正整数)' }
  }
  const userId = Number(form.userId.trim())
  if (!form.userId.trim() || !Number.isInteger(userId) || userId <= 0) {
    return { error: '请填写有效的用户 ID(正整数)' }
  }
  const parsed = parseAmountToCents(form.amount)
  if ('error' in parsed) return { error: parsed.error }
  const providerKind = form.providerKind === 'taobao' ? 'taobao' : 'manual'
  const outTradeNo = buildOutTradeNo(tenantId, userId, parsed.cents, String(nowMs))
  return { tenantId, userId, amountCents: parsed.cents, outTradeNo, providerKind }
}
