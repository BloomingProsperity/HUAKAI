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

/*
 * 充值配置展示纯逻辑(可单测)。仅用于只读渲染,不参与开单。
 */

/** 支付渠道(后端已小写归一)→ 中文展示名;未知渠道回落原值(首字母大写)。 */
const PROVIDER_LABELS: Record<string, string> = {
  manual: '人工转账',
  taobao: '淘宝/闲鱼',
  alipay: '支付宝',
  wechat: '微信支付',
}

export function providerLabel(provider: string): string {
  const key = provider.trim().toLowerCase()
  const hit = PROVIDER_LABELS[key]
  if (hit) return hit
  if (key === '') return '未知渠道'
  return key.charAt(0).toUpperCase() + key.slice(1)
}

/**
 * 把金额区间(分)格式化成带币种符号的可读区间,如 (100, 500000, "USD") → "$1.00 ~ $5000.00"。
 * 非 USD 币种用「金额 币种码」形式回落(如 CNY → "1.00 CNY ~ ...")。
 */
export function formatCentsRange(minCents: number, maxCents: number, currencyCode: string): string {
  const sym = currencyCode === 'USD' ? '$' : ''
  const suffix = sym === '' ? ` ${currencyCode}` : ''
  const fmt = (c: number) => `${sym}${formatMoney(c)}${suffix}`
  return `${fmt(minCents)} ~ ${fmt(maxCents)}`
}
