/*
 * 钱包纯逻辑(可单测)。金额分→元(避免浮点,整数运算 + 补零),订单状态配色。
 */

/** 分→带符号的元字符串(如 1240 → "12.40",-250 → "-2.50")。 */
export function formatMoney(cents: number): string {
  if (!Number.isFinite(cents)) return '0.00'
  const neg = cents < 0
  const c = Math.abs(Math.trunc(cents))
  const s = `${Math.floor(c / 100)}.${String(c % 100).padStart(2, '0')}`
  return neg ? `-${s}` : s
}

export type Tone = 'ok' | 'warn' | 'danger' | 'muted'

/** 订单状态 → 配色 tone。 */
export function orderStatusTone(status: string): Tone {
  switch (status) {
    case 'completed':
    case 'paid':
      return 'ok'
    case 'pending':
    case 'recharging':
      return 'warn'
    case 'failed':
    case 'cancelled':
    case 'canceled':
    case 'refunded':
      return 'danger'
    default:
      return 'muted'
  }
}

const STATUS_LABELS: Record<string, string> = {
  pending: '待支付',
  paid: '已支付',
  recharging: '入账中',
  completed: '已完成',
  failed: '失败',
  cancelled: '已取消',
  canceled: '已取消',
  refunded: '已退款',
}

export function orderStatusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status
}

/** 充值单(order_kind=topup)且已完成的入账总额(分)。 */
export function completedTopupCents(orders: Array<{ order_kind: string; status: string; amount_cents: number }>): number {
  return orders
    .filter((o) => o.order_kind === 'topup' && o.status === 'completed')
    .reduce((sum, o) => sum + o.amount_cents, 0)
}
