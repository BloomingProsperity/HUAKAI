import { describe, expect, it } from 'vitest'
import { completedTopupCents, formatMoney, mapWalletOrderRows, orderStatusLabel, orderStatusTone } from './wallet'

describe('formatMoney', () => {
  it('分→元补零,带符号', () => {
    // 判别核心:补零 + 整数运算。变异(去 padStart)→ 5 分得 "0.5" 而非 "0.05" → RED。
    expect(formatMoney(1240)).toBe('12.40')
    expect(formatMoney(5)).toBe('0.05')
    expect(formatMoney(0)).toBe('0.00')
    expect(formatMoney(100000)).toBe('1000.00')
    expect(formatMoney(-250)).toBe('-2.50')
  })
})

describe('orderStatusTone / label', () => {
  it('状态映射配色', () => {
    expect(orderStatusTone('completed')).toBe('ok')
    expect(orderStatusTone('pending')).toBe('warn')
    expect(orderStatusTone('failed')).toBe('danger')
    expect(orderStatusTone('weird')).toBe('muted')
  })
  it('中文状态标', () => {
    expect(orderStatusLabel('pending')).toBe('待支付')
    expect(orderStatusLabel('completed')).toBe('已完成')
  })
})

describe('completedTopupCents', () => {
  it('只累计已完成的充值单(topup+completed)', () => {
    // 判别核心:必须同时过滤 order_kind=topup 与 status=completed。
    // 变异(不过滤 status)→ 把 pending 也算进去 → RED。
    const orders = [
      { order_kind: 'topup', status: 'completed', amount_cents: 5000 },
      { order_kind: 'topup', status: 'pending', amount_cents: 9999 }, // 未完成,不算
      { order_kind: 'subscription', status: 'completed', amount_cents: 3000 }, // 非充值,不算
      { order_kind: 'topup', status: 'completed', amount_cents: 1200 },
    ]
    expect(completedTopupCents(orders)).toBe(6200)
  })
})

describe('mapWalletOrderRows', () => {
  it('完整映射订单五列且保留稳定行键', () => {
    const createdAt = '2026-07-13T09:10:11Z'
    // 判别核心:类型、分转金额、状态标签/语气和时间必须全部来自同一订单。
    expect(mapWalletOrderRows([{
      id: 7,
      out_trade_no: 'TOPUP-7',
      order_kind: 'topup',
      amount_cents: 1234,
      status: 'completed',
      created_at: createdAt,
    }])).toEqual([{
      id: 7,
      tradeNo: 'TOPUP-7',
      kind: '充值',
      amount: '$12.34',
      statusLabel: '已完成',
      statusTone: 'ok',
      createdAt: new Date(createdAt).toLocaleString(),
    }])
  })

  it('未知订单类型与状态不被伪装成已知值', () => {
    const [row] = mapWalletOrderRows([{
      id: 8,
      out_trade_no: 'OTHER-8',
      order_kind: 'credit_adjustment',
      amount_cents: 5,
      status: 'custom',
      created_at: '2026-07-13T00:00:00Z',
    }])
    expect(row.kind).toBe('credit_adjustment')
    expect(row.statusLabel).toBe('custom')
    expect(row.statusTone).toBe('muted')
  })
})
