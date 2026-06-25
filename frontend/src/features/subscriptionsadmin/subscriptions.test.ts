import { describe, expect, it } from 'vitest'
import {
  buildPlanRequest,
  centsToUsd,
  planStatusLabel,
  planToForm,
  planTone,
  subscriptionTone,
  usdToCents,
} from './subscriptions'
import { EMPTY_PLAN_FORM, type Plan, type PlanFormState } from './types'

function form(over: Partial<PlanFormState>): PlanFormState {
  return { ...EMPTY_PLAN_FORM, ...over }
}

function plan(over: Partial<Plan>): Plan {
  return {
    id: 1,
    tenant_id: 1,
    name: '基础版',
    price_cents: 1999,
    currency_code: 'USD',
    validity_days: 30,
    for_sale: true,
    enabled: true,
    sort_order: 0,
    created_at: '2026-06-25T00:00:00Z',
    updated_at: '2026-06-25T00:00:00Z',
    ...over,
  }
}

describe('usdToCents', () => {
  it('美元字符串四舍五入到分', () => {
    // 判别核心:19.99 美元 = 1999 分。变异(改 *100 为 *10 或去 round)→ 此断言 RED。
    expect(usdToCents('19.99')).toBe(1999)
    expect(usdToCents('0')).toBe(0)
    expect(usdToCents('5')).toBe(500)
  })
  it('空串视为 0', () => {
    expect(usdToCents('')).toBe(0)
    expect(usdToCents('   ')).toBe(0)
  })
  it('非法/负数 → null', () => {
    // 判别核心:必须拒绝非十进制与负数。变异(放宽正则)→ 这些断言 RED。
    expect(usdToCents('-1')).toBeNull()
    expect(usdToCents('1e3')).toBeNull()
    expect(usdToCents('abc')).toBeNull()
    expect(usdToCents('0x10')).toBeNull()
  })
})

describe('centsToUsd', () => {
  it('分 → 两位小数美元', () => {
    expect(centsToUsd(1999)).toBe('19.99')
    expect(centsToUsd(0)).toBe('0.00')
    expect(centsToUsd(500)).toBe('5.00')
  })
})

describe('buildPlanRequest', () => {
  it('合法表单 → 请求体(分换算 + 默认值)', () => {
    const r = buildPlanRequest(form({ name: ' 专业版 ', priceUsd: '49.99', validityDays: '30' }), 7)
    expect(r.ok).toBe(true)
    if (!r.ok) return
    // 判别核心:name trim、price 转分、tenant_id 透传。
    expect(r.request.name).toBe('专业版')
    expect(r.request.price_cents).toBe(4999)
    expect(r.request.tenant_id).toBe(7)
    expect(r.request.validity_days).toBe(30)
    expect(r.request.for_sale).toBe(true)
  })

  it('name 必填', () => {
    const r = buildPlanRequest(form({ name: '  ' }), 1)
    expect(r.ok).toBe(false)
  })

  it('有效天数必须为正整数', () => {
    // 判别核心:0/负/小数天数必须拒。变异(去掉正整数校验)→ RED。
    expect(buildPlanRequest(form({ name: 'x', validityDays: '0' }), 1).ok).toBe(false)
    expect(buildPlanRequest(form({ name: 'x', validityDays: '-5' }), 1).ok).toBe(false)
    expect(buildPlanRequest(form({ name: 'x', validityDays: '1.5' }), 1).ok).toBe(false)
  })

  it('价格非法 → 失败', () => {
    expect(buildPlanRequest(form({ name: 'x', priceUsd: '-1' }), 1).ok).toBe(false)
  })

  it('空 cap 字段不下发(只带填了的)', () => {
    // 判别核心:空封顶必须省略(对齐后端 parseCap 空串=不限)。变异(无条件赋值)→ RED。
    const r = buildPlanRequest(
      form({ name: 'x', dailyCapUsd: '10', weeklyCapUsd: '', monthlyCapUsd: '  ' }),
      1,
    )
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.request.daily_cap_usd).toBe('10')
    expect('weekly_cap_usd' in r.request).toBe(false)
    expect('monthly_cap_usd' in r.request).toBe(false)
  })

  it('description/granted_group 空串省略', () => {
    const r = buildPlanRequest(form({ name: 'x' }), 1)
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect('description' in r.request).toBe(false)
    expect('granted_group' in r.request).toBe(false)
  })

  it('for_sale=false 透传(区分省略与显式 false)', () => {
    const r = buildPlanRequest(form({ name: 'x', forSale: false }), 1)
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.request.for_sale).toBe(false)
  })
})

describe('planToForm', () => {
  it('套餐回填表单(分→美元、null cap→空串)', () => {
    const f = planToForm(plan({ price_cents: 4999, daily_cap_usd: '12.5', weekly_cap_usd: null }))
    expect(f.priceUsd).toBe('49.99')
    expect(f.dailyCapUsd).toBe('12.5')
    expect(f.weeklyCapUsd).toBe('')
    expect(f.forSale).toBe(true)
  })
})

describe('subscriptionTone', () => {
  it('active→ok,pending→warn,cancelled→danger,其余→muted', () => {
    expect(subscriptionTone('active')).toBe('ok')
    expect(subscriptionTone('PENDING')).toBe('warn')
    expect(subscriptionTone('cancelled')).toBe('danger')
    expect(subscriptionTone('expired')).toBe('danger')
    expect(subscriptionTone('weird')).toBe('muted')
  })
})

describe('planTone / planStatusLabel', () => {
  it('停用优先 danger;在售 ok;未上架 muted', () => {
    // 判别核心:enabled=false 不论 for_sale 都 danger。变异(去 enabled 判断)→ RED。
    expect(planTone(plan({ enabled: false, for_sale: true }))).toBe('danger')
    expect(planTone(plan({ enabled: true, for_sale: true }))).toBe('ok')
    expect(planTone(plan({ enabled: true, for_sale: false }))).toBe('muted')
    expect(planStatusLabel(plan({ enabled: false }))).toBe('已停用')
    expect(planStatusLabel(plan({ enabled: true, for_sale: true }))).toBe('在售')
    expect(planStatusLabel(plan({ enabled: true, for_sale: false }))).toBe('未上架')
  })
})
