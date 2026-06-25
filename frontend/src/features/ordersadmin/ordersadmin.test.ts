import { describe, expect, it } from 'vitest'
import {
  buildOrderListQuery,
  EMPTY_ORDER_FILTER,
  formatCents,
  hasAnyAction,
  orderActions,
  statusLabel,
  statusTone,
  toRfc3339,
  type OrderFilterForm,
} from './ordersadmin'

function filter(over: Partial<OrderFilterForm>): OrderFilterForm {
  return { ...EMPTY_ORDER_FILTER, ...over }
}

describe('buildOrderListQuery', () => {
  it('tenant_id 缺失 / 非正 → 报错(后端硬性必填,前端先拦)', () => {
    // 判别核心:无 tenant_id 必须报错。变异(允许空 tenant 通过)→ 此断言 RED。
    expect(buildOrderListQuery(filter({}), 50, 0)).toEqual({ error: '请填写有效的租户 ID(正整数)' })
    expect(buildOrderListQuery(filter({ tenantId: '0' }), 50, 0)).toEqual({ error: '请填写有效的租户 ID(正整数)' })
    expect(buildOrderListQuery(filter({ tenantId: '-3' }), 50, 0)).toEqual({ error: '请填写有效的租户 ID(正整数)' })
  })

  it('仅 tenant_id 时,空筛选字段全部省略(不污染 query)', () => {
    const r = buildOrderListQuery(filter({ tenantId: '7' }), 50, 0)
    expect('query' in r).toBe(true)
    const q = (r as { query: Record<string, unknown> }).query
    expect(q).toEqual({ tenant_id: 7, limit: 50, offset: 0 })
    // 判别核心:status/user_id/时间为空时绝不出现在 query 里。
    expect('status' in q).toBe(false)
    expect('user_id' in q).toBe(false)
    expect('created_from' in q).toBe(false)
  })

  it('用户 ID 非正 → 报错', () => {
    expect(buildOrderListQuery(filter({ tenantId: '7', userId: '0' }), 50, 0)).toEqual({ error: '用户 ID 必须为正整数' })
  })

  it('齐全字段全部带上,时间转 RFC3339', () => {
    const r = buildOrderListQuery(
      filter({ tenantId: '7', userId: '9', status: 'pending', createdFrom: '2026-01-01T00:00' }),
      20,
      40,
    )
    const q = (r as { query: Record<string, unknown> }).query
    expect(q.tenant_id).toBe(7)
    expect(q.user_id).toBe(9)
    expect(q.status).toBe('pending')
    expect(q.limit).toBe(20)
    expect(q.offset).toBe(40)
    expect(String(q.created_from)).toMatch(/^\d{4}-\d{2}-\d{2}T.*Z$/)
  })
})

describe('orderActions 状态机', () => {
  it('pending 可确认+可取消,不可重试', () => {
    // 判别核心:pending 必须可取消。变异(canCancel 恒 false)→ RED。
    expect(orderActions('pending')).toEqual({ canConfirm: true, canCancel: true, canRetry: false })
  })

  it('paid 可确认+可重试,不可取消', () => {
    // 判别核心:paid 不可取消(撤单只针对未支付挂单)。变异(canCancel 恒 true)→ RED。
    expect(orderActions('paid')).toEqual({ canConfirm: true, canCancel: false, canRetry: true })
  })

  it('recharging 仅可重试', () => {
    expect(orderActions('recharging')).toEqual({ canConfirm: false, canCancel: false, canRetry: true })
  })

  it('终止态(completed/refunded/cancelled/failed/expired)无任何动作', () => {
    for (const s of ['completed', 'refunded', 'cancelled', 'failed', 'expired']) {
      expect(orderActions(s)).toEqual({ canConfirm: false, canCancel: false, canRetry: false })
      expect(hasAnyAction(s)).toBe(false)
    }
  })

  it('pending/paid/recharging 至少有一个动作', () => {
    expect(hasAnyAction('pending')).toBe(true)
    expect(hasAnyAction('paid')).toBe(true)
    expect(hasAnyAction('recharging')).toBe(true)
  })
})

describe('statusLabel / statusTone', () => {
  it('已知状态给中文,未知状态回落原值', () => {
    expect(statusLabel('completed')).toBe('已完成')
    expect(statusLabel('weird')).toBe('weird')
  })
  it('completed=ok,failed=danger', () => {
    // 判别核心:failed 必须 danger。变异(返回 ok)→ RED。
    expect(statusTone('completed')).toBe('ok')
    expect(statusTone('failed')).toBe('danger')
  })
})

describe('formatCents', () => {
  it('整数分转两位小数,无浮点误差', () => {
    // 判别核心:1999 cents = 19.99 而非 19.9899999。变异(用 /100 浮点)易现误差。
    expect(formatCents(1999, 'CNY')).toBe('19.99 CNY')
    expect(formatCents(5, 'CNY')).toBe('0.05 CNY')
    expect(formatCents(-1234, 'USD')).toBe('-12.34 USD')
  })
})

describe('toRfc3339', () => {
  it('空串 → 空串;非法 → 空串', () => {
    expect(toRfc3339('')).toBe('')
    expect(toRfc3339('not-a-date')).toBe('')
  })
})
