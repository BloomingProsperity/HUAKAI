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
