/*
 * 我的订单纯逻辑(可单测,无 IO 无 DOM):
 *   - 订单状态机:状态枚举 / 中文标签 / 徽章语气;
 *   - money 展示:amount_cents → 「X.XX <币种>」(只读格式化,绝不参与金额裁决);
 *   - 列表筛选:客户端按状态过滤(后端列表端点不收 status 参数,真码 handler.go:310 只读 limit);
 *   - limit 守卫:对齐后端 parseLimit(1-200,越界回落 50;真码 handler.go:390);
 *   - 状态机时间线:把订单的各时间戳字段拼成「已发生 / 未发生」步骤,供详情页可视化。
 *
 * 状态枚举与后端真码严格对齐:payment/types.go:28 StatusPending..StatusFailed(共 8 态)。
 */
import type { BadgeTone } from '../../ui/StatusBadge'
import type { UserOrder } from './types'

/** 后端订单状态机的 8 个状态(payment/types.go:28)。 */
export const ORDER_STATUSES: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'pending', label: '待支付' },
  { value: 'paid', label: '已支付' },
  { value: 'recharging', label: '入账中' },
  { value: 'completed', label: '已完成' },
  { value: 'refunded', label: '已退款' },
  { value: 'expired', label: '已过期' },
  { value: 'cancelled', label: '已取消' },
  { value: 'failed', label: '失败' },
]

/** 状态 → 中文标签;未知状态原样回显(不吞掉后端新增态)。 */
export function statusLabel(status: string): string {
  return ORDER_STATUSES.find((s) => s.value === status)?.label ?? status
}

/** 状态 → 徽章语气。完成=ok,推进中=info,待支付=warn,失败=danger,其余终止态=muted。 */
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

/** 订单种类 → 中文标签(后端 order_kind:topup / subscription;空值视作充值)。 */
export function orderKindLabel(kind: string): string {
  switch (kind) {
    case 'subscription':
      return '订阅'
    case 'topup':
    case '':
      return '充值'
    default:
      return kind
  }
}

/** 支付渠道 → 中文标签(后端 provider_kind:manual / taobao,真码 payment/types.go:43)。 */
export function providerLabel(kind: string): string {
  switch (kind) {
    case 'manual':
      return '手动转账'
    case 'taobao':
      return '淘宝/闲鱼'
    default:
      return kind
  }
}

/**
 * amount_cents 格式化为「X.XX <币种>」只读展示串。
 * 负/非有限数回落到 0.00,避免把脏数据当真金渲染。币种取后端 currency_code(默认 USD)。
 */
export function formatMoney(cents: number, currency: string): string {
  const code = currency.trim() || 'USD'
  if (!Number.isFinite(cents)) return `0.00 ${code}`
  return `${(cents / 100).toFixed(2)} ${code}`
}

/** 守卫列表 limit:对齐后端 parseLimit(1-200,越界/非整数回落 50)。 */
export function clampLimit(n: number): number {
  if (!Number.isInteger(n) || n <= 0 || n > 200) return 50
  return n
}

/**
 * 客户端按状态筛选订单。status 为空串=不过滤(返回全部)。
 * 后端列表端点不收 status 参数,故筛选只在已拉取窗口内做(真码 handler.go:310)。
 */
export function filterByStatus(orders: ReadonlyArray<UserOrder>, status: string): UserOrder[] {
  if (!status) return [...orders]
  return orders.filter((o) => o.status === status)
}

/** 各状态在窗口内的订单计数(用于筛选条上的角标)。 */
export function statusCounts(orders: ReadonlyArray<UserOrder>): Record<string, number> {
  const counts: Record<string, number> = {}
  for (const o of orders) {
    counts[o.status] = (counts[o.status] ?? 0) + 1
  }
  return counts
}

export interface OrderTableRow {
  id: number
  tradeNo: string
  kind: string
  amount: string
  provider: string
  status: string
  tone: BadgeTone
  createdAt: string
  canCancel: boolean
  canRefund: boolean
}

/** 把订单 DTO 映射为列表列值；金额只做展示格式化，动作资格仍复用原有门槛。 */
export function mapOrderTableRows(orders: ReadonlyArray<UserOrder>): OrderTableRow[] {
  return orders.map((order) => ({
    id: order.id,
    tradeNo: order.out_trade_no,
    kind: orderKindLabel(order.order_kind),
    amount: formatMoney(order.amount_cents, order.currency_code),
    provider: providerLabel(order.provider_kind),
    status: statusLabel(order.status),
    tone: statusTone(order.status),
    createdAt: formatOrderTime(order.created_at),
    canCancel: cancellable(order),
    canRefund: refundRequestable(order),
  }))
}

export function formatOrderTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString('zh-CN', { hour12: false })
}

/**
 * 订单是否可下载收据 —— 与后端 invoicehttp/handler.go:60,64 的判定严格对齐:
 *   ① order_kind ∈ {topup, subscription}(receiptEligibleKind);
 *   ② status === 'completed'(仅已完成订单才出收据)。
 * 两者皆满足才显示「下载收据」入口;否则后端会回 404/409,前端先行隐藏避免无效请求。
 * 判别核心:必须同时校验 kind 与 status(变异成只看其一 → 对未完成/不合格订单也露入口 → RED)。
 */
export function receiptEligible(order: Pick<UserOrder, 'order_kind' | 'status'>): boolean {
  const kindOk = order.order_kind === 'topup' || order.order_kind === 'subscription'
  return kindOk && order.status === 'completed'
}

/**
 * 订单可执行的用户自助动作判定(与后端前置门槛严格对齐):
 *   - cancellable:仅 pending 单可撤(对齐 newUserCancelHandler → CancelOrder,非 pending 回 409 order_not_cancelable)。
 *   - refundRequestable:仅「已完成的充值单(topup+completed)」可发起退款申请
 *     (对齐 user_portal.go:451:order_kind=topup 且 status=completed,否则回 409 order_not_refund_requestable)。
 * 前端先行过滤,避免对不合格订单露出动作按钮触发无效 409 请求。
 * 判别核心:
 *   - cancellable 必须只认 pending(变异成放开其它态 → 已完成/已取消单也露撤单 → RED);
 *   - refundRequestable 必须同时卡 kind 与 status(变异成只看其一 → 订阅单或未完成单露退款 → RED)。
 */
export function cancellable(order: Pick<UserOrder, 'status'>): boolean {
  return order.status === 'pending'
}

export function refundRequestable(order: Pick<UserOrder, 'order_kind' | 'status'>): boolean {
  return order.order_kind === 'topup' && order.status === 'completed'
}

/** 任一自助动作可用(撤单或退款),用于决定是否渲染动作列/区块。 */
export function hasUserAction(order: Pick<UserOrder, 'order_kind' | 'status'>): boolean {
  return cancellable(order) || refundRequestable(order)
}

/** 时间线一步:label=步骤名,at=发生时刻(null=尚未发生),done=是否已发生。 */
export interface TimelineStep {
  key: string
  label: string
  at: string | null
  done: boolean
}

/**
 * 把单张订单拼成状态机时间线(供详情页可视化)。
 * 步骤取自订单的时间戳字段:下单→支付→完成。终止/旁路态(退款/过期/取消/失败)
 * 没有独立时间戳字段时,以当前 status 作为末步补一行,让用户看到订单最终归宿。
 */
export function buildTimeline(order: UserOrder): TimelineStep[] {
  const steps: TimelineStep[] = [
    { key: 'created', label: '已下单', at: order.created_at ?? null, done: !!order.created_at },
    { key: 'paid', label: '已支付', at: order.paid_at ?? null, done: !!order.paid_at },
    { key: 'completed', label: '已完成', at: order.completed_at ?? null, done: !!order.completed_at },
  ]
  // 终止/旁路态(无专属时间戳):以 updated_at 作为发生时刻补一行,标注当前归宿。
  const terminal: Record<string, string> = {
    refunded: '已退款',
    expired: '已过期',
    cancelled: '已取消',
    failed: '失败',
  }
  const label = terminal[order.status]
  if (label) {
    steps.push({ key: order.status, label, at: order.updated_at ?? null, done: true })
  }
  return steps
}
