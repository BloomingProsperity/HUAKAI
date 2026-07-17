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

export interface WalletOrderSource {
  id: number
  out_trade_no: string
  order_kind: string
  amount_cents: number
  status: string
  created_at: string
}

export interface WalletOrderTableRow {
  id: number
  tradeNo: string
  kind: string
  amount: string
  statusLabel: string
  statusTone: Tone
  createdAt: string
}

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

/** 最近订单到展示行的纯映射，集中固定类型、金额、状态和时间列语义。 */
export function mapWalletOrderRows(orders: WalletOrderSource[]): WalletOrderTableRow[] {
  return orders.map((order) => ({
    id: order.id,
    tradeNo: order.out_trade_no,
    kind: order.order_kind === 'topup' ? '充值' : order.order_kind === 'subscription' ? '订阅' : order.order_kind,
    amount: `$${formatMoney(order.amount_cents)}`,
    statusLabel: orderStatusLabel(order.status),
    statusTone: orderStatusTone(order.status),
    createdAt: new Date(order.created_at).toLocaleString(),
  }))
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

/*
 * 充值开单纯逻辑(money 敏感,可单测,无 IO 无 DOM)。
 * 金额输入按「美元」字符串解析成「分」(避免浮点:整数化后乘 100),
 * 再与门户配置区间做客户端前置校验。服务端仍会二次裁决金额(真码 user_portal.go:315),
 * 这里只是早失败 + 给清晰提示,绝不作为最终金额权威。
 */

/** 金额解析/校验结果:ok=true 时 amountCents 为正整数(分);否则 error 为中文原因。 */
export type TopupAmountResult =
  | { ok: true; amountCents: number }
  | { ok: false; error: string }

/**
 * 把用户输入的「美元」金额字符串解析成「分」并按 [minCents, maxCents] 校验。
 * 规则:
 *   - 必须是非负数字字面量,至多两位小数(美分精度);多于两位小数判非法。
 *   - 解析成分时用四舍五入到整数分(`Math.round(dollars*100)`)规避 2.5→249 这类浮点漂移。
 *   - 金额必须 > 0 且落在配置区间内(含端点);越界返回带可读区间的中文提示。
 * 判别核心:区间两端都要卡(变异成只卡一端 → 越界金额漏过 → RED);
 *           两位小数限制要在(变异成放开 → "1.005" 被吞成 100/101 分 → RED)。
 */
export function parseTopupAmount(
  input: string,
  minCents: number,
  maxCents: number,
  currencyCode = 'USD',
): TopupAmountResult {
  const raw = input.trim()
  if (raw === '') return { ok: false, error: '请输入充值金额' }
  // 仅允许可选符号? 不:充值金额必须为正,显式拒负号与科学计数法。
  if (!/^\d+(\.\d{1,2})?$/.test(raw)) {
    return { ok: false, error: '金额格式不合法(最多两位小数,且必须为正数)' }
  }
  const dollars = Number(raw)
  if (!Number.isFinite(dollars)) return { ok: false, error: '金额格式不合法' }
  const amountCents = Math.round(dollars * 100)
  if (amountCents <= 0) return { ok: false, error: '充值金额必须大于 0' }
  if (amountCents < minCents || amountCents > maxCents) {
    return {
      ok: false,
      error: `金额需在 ${formatCentsRange(minCents, maxCents, currencyCode)} 之间`,
    }
  }
  return { ok: true, amountCents }
}
