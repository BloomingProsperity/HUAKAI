import { describe, expect, it } from 'vitest'
import {
  buildTimeline,
  cancellable,
  clampLimit,
  filterByStatus,
  formatMoney,
  hasUserAction,
  mapOrderTableRows,
  orderKindLabel,
  providerLabel,
  receiptEligible,
  refundRequestable,
  statusCounts,
  statusLabel,
  statusTone,
} from './orders'
import type { UserOrder } from './types'

/** 构造一个最小订单(仅填测试关心的字段,其余给默认)。 */
function order(over: Partial<UserOrder>): UserOrder {
  return {
    id: 1,
    out_trade_no: 'T-1',
    user_id: 9,
    amount_cents: 100,
    currency_code: 'USD',
    status: 'pending',
    provider_kind: 'manual',
    order_kind: 'topup',
    created_at: '2026-06-25T00:00:00Z',
    updated_at: '2026-06-25T00:00:00Z',
    ...over,
  }
}

describe('receiptEligible', () => {
  it('已完成的充值/订阅订单 → 可下载收据', () => {
    expect(receiptEligible(order({ order_kind: 'topup', status: 'completed' }))).toBe(true)
    expect(receiptEligible(order({ order_kind: 'subscription', status: 'completed' }))).toBe(true)
  })
  it('未完成订单 → 不可(即便种类合格)', () => {
    // 判别核心:必须同时校验 status=completed(变异成只看 kind → 待支付订单也露入口 → RED)。
    expect(receiptEligible(order({ order_kind: 'topup', status: 'pending' }))).toBe(false)
    expect(receiptEligible(order({ order_kind: 'topup', status: 'paid' }))).toBe(false)
  })
  it('种类不合格 → 不可(即便已完成)', () => {
    // 判别核心:必须同时校验 kind(变异成只看 status → 非充值/订阅订单也出收据 → RED)。
    expect(receiptEligible(order({ order_kind: 'other', status: 'completed' }))).toBe(false)
  })
})

describe('cancellable / refundRequestable / hasUserAction(动作门槛)', () => {
  it('cancellable 仅 pending 单', () => {
    // 判别核心:只有 pending 可撤。变异(放开 paid/completed)→ 已支付/已完成单也露撤单 → RED。
    expect(cancellable(order({ status: 'pending' }))).toBe(true)
    expect(cancellable(order({ status: 'paid' }))).toBe(false)
    expect(cancellable(order({ status: 'completed' }))).toBe(false)
    expect(cancellable(order({ status: 'cancelled' }))).toBe(false)
  })
  it('refundRequestable 仅「已完成的充值单」(topup+completed)', () => {
    // 判别核心:必须同时卡 kind=topup 与 status=completed。
    expect(refundRequestable(order({ order_kind: 'topup', status: 'completed' }))).toBe(true)
    // 变异(只看 status)→ 订阅完成单也露退款 → RED。
    expect(refundRequestable(order({ order_kind: 'subscription', status: 'completed' }))).toBe(false)
    // 变异(只看 kind)→ 未完成充值单也露退款 → RED。
    expect(refundRequestable(order({ order_kind: 'topup', status: 'pending' }))).toBe(false)
    expect(refundRequestable(order({ order_kind: 'topup', status: 'paid' }))).toBe(false)
  })
  it('hasUserAction 在可撤或可退款时为真', () => {
    expect(hasUserAction(order({ status: 'pending' }))).toBe(true) // 可撤
    expect(hasUserAction(order({ order_kind: 'topup', status: 'completed' }))).toBe(true) // 可退款
    // 判别核心:既不可撤也不可退款 → false。变异(恒返回 true)→ 失败单也露动作 → RED。
    expect(hasUserAction(order({ order_kind: 'topup', status: 'failed' }))).toBe(false)
    expect(hasUserAction(order({ order_kind: 'subscription', status: 'completed' }))).toBe(false)
  })
})

describe('statusLabel / statusTone', () => {
  it('已知状态译中文,未知原样回显', () => {
    expect(statusLabel('completed')).toBe('已完成')
    expect(statusLabel('recharging')).toBe('入账中')
    // 判别核心:未知状态必须原样返回,不能被吞成空/默认。变异(恒返回固定串)→ RED。
    expect(statusLabel('weird_new')).toBe('weird_new')
  })
  it('完成=ok,待支付=warn,失败=danger', () => {
    expect(statusTone('completed')).toBe('ok')
    // 判别核心:失败必须 danger(红)。变异(failed 漏掉落到 default=muted)→ RED。
    expect(statusTone('failed')).toBe('danger')
    expect(statusTone('pending')).toBe('warn')
    expect(statusTone('paid')).toBe('info')
  })
})

describe('orderKindLabel / providerLabel', () => {
  it('种类与渠道译中文', () => {
    expect(orderKindLabel('subscription')).toBe('订阅')
    expect(orderKindLabel('')).toBe('充值')
    expect(providerLabel('taobao')).toBe('淘宝/闲鱼')
  })
})

describe('formatMoney', () => {
  it('分→两位小数带币种', () => {
    // 判别核心:1999 分必须是 19.99(除以 100),不是 1999。变异(漏 /100)→ RED。
    expect(formatMoney(1999, 'USD')).toBe('19.99 USD')
    expect(formatMoney(100, 'USD')).toBe('1.00 USD')
  })
  it('币种空串回落 USD;非有限数回落 0.00', () => {
    expect(formatMoney(500, '')).toBe('5.00 USD')
    expect(formatMoney(Number.NaN, 'USD')).toBe('0.00 USD')
  })
})

describe('clampLimit', () => {
  it('1-200 内原样,越界/非整数回落 50', () => {
    expect(clampLimit(100)).toBe(100)
    // 判别核心:超 200 必须回落 50(对齐后端)。变异(直接返回 n)→ RED。
    expect(clampLimit(500)).toBe(50)
    expect(clampLimit(0)).toBe(50)
    expect(clampLimit(1.5)).toBe(50)
  })
})

describe('filterByStatus', () => {
  const orders = [
    order({ id: 1, status: 'completed' }),
    order({ id: 2, status: 'pending' }),
    order({ id: 3, status: 'completed' }),
  ]
  it('空筛选返回全部(副本)', () => {
    const out = filterByStatus(orders, '')
    expect(out).toHaveLength(3)
    expect(out).not.toBe(orders) // 返回新数组,不泄露原引用
  })
  it('按状态过滤', () => {
    // 判别核心:只留匹配状态。变异(忽略 status 返回全部)→ RED。
    const out = filterByStatus(orders, 'completed')
    expect(out.map((o) => o.id)).toEqual([1, 3])
  })
})

describe('statusCounts', () => {
  it('按状态计数', () => {
    const counts = statusCounts([
      order({ status: 'completed' }),
      order({ status: 'completed' }),
      order({ status: 'failed' }),
    ])
    expect(counts.completed).toBe(2)
    expect(counts.failed).toBe(1)
  })
})

describe('mapOrderTableRows', () => {
  it('六列展示值与动作门槛逐项映射，不能串列或放宽撤单', () => {
    const rows = mapOrderTableRows([
      order({ id: 7, out_trade_no: 'T-7', order_kind: 'subscription', amount_cents: 1999, currency_code: 'CNY', provider_kind: 'taobao', status: 'pending' }),
      order({ id: 8, out_trade_no: 'T-8', order_kind: 'topup', amount_cents: 250, status: 'completed' }),
    ])
    expect(rows[0]).toMatchObject({
      id: 7,
      tradeNo: 'T-7',
      kind: '订阅',
      amount: '19.99 CNY',
      provider: '淘宝/闲鱼',
      status: '待支付',
      tone: 'warn',
      canCancel: true,
      canRefund: false,
    })
    expect(rows[0].createdAt).not.toBe('')
    expect(rows[1]).toMatchObject({ id: 8, canCancel: false, canRefund: true })
  })
})

describe('buildTimeline', () => {
  it('按时间戳标已发生/未发生', () => {
    const t = buildTimeline(
      order({ status: 'paid', created_at: '2026-06-25T00:00:00Z', paid_at: '2026-06-25T01:00:00Z' }),
    )
    const created = t.find((s) => s.key === 'created')!
    const paid = t.find((s) => s.key === 'paid')!
    const completed = t.find((s) => s.key === 'completed')!
    expect(created.done).toBe(true)
    expect(paid.done).toBe(true)
    // 判别核心:无 completed_at 的步骤必须 done=false。变异(恒 done=true)→ RED。
    expect(completed.done).toBe(false)
    expect(completed.at).toBeNull()
  })
  it('终止态(退款/取消等)补一行末步', () => {
    const t = buildTimeline(order({ status: 'refunded', updated_at: '2026-06-25T05:00:00Z' }))
    const last = t[t.length - 1]
    expect(last.key).toBe('refunded')
    expect(last.label).toBe('已退款')
    expect(last.at).toBe('2026-06-25T05:00:00Z')
  })
  it('正常推进态(pending/paid/completed)不补末步', () => {
    const t = buildTimeline(order({ status: 'completed', completed_at: '2026-06-25T02:00:00Z' }))
    expect(t).toHaveLength(3) // created/paid/completed,无额外终止行
  })
})
