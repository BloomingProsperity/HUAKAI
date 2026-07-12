import { describe, expect, it } from 'vitest'
import {
  actorLabel,
  auditEventLabel,
  buildExtendRequest,
  buildPlanRequest,
  buildVoucherRequest,
  centsToUsd,
  EMPTY_VOUCHER_FORM,
  parseBulkUserIDs,
  planStatusLabel,
  planToForm,
  planTone,
  subscriptionTone,
  usdToCents,
  type VoucherFormState,
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

describe('parseBulkUserIDs', () => {
  it('多分隔符解析 + 去重', () => {
    // 判别核心:逗号/空格/换行混合分隔都能切出 token,且重复 ID 去重。
    const r = parseBulkUserIDs('1, 2\n3  2,1')
    expect('ids' in r).toBe(true)
    if (!('ids' in r)) return
    expect(r.ids).toEqual([1, 2, 3])
  })
  it('任一非正整数 → 整体失败', () => {
    // 判别核心:遇到非法 token 必须整体报错,不能静默吞掉(否则误分配)。
    // 变异(把 return error 改成 continue)→ 这些断言 RED。
    expect('error' in parseBulkUserIDs('1, abc, 3')).toBe(true)
    expect('error' in parseBulkUserIDs('1, -2')).toBe(true)
    expect('error' in parseBulkUserIDs('1, 0')).toBe(true)
    expect('error' in parseBulkUserIDs('1, 2.5')).toBe(true)
  })
  it('全空 → 失败', () => {
    expect('error' in parseBulkUserIDs('   ')).toBe(true)
    expect('error' in parseBulkUserIDs('')).toBe(true)
  })
})

describe('buildExtendRequest', () => {
  const now = Date.parse('2026-06-29T00:00:00Z')
  it('days 模式:正整数 → 仅下发 days', () => {
    const r = buildExtendRequest('days', 1, '30', '', now)
    expect('request' in r).toBe(true)
    if (!('request' in r)) return
    expect(r.request.days).toBe(30)
    // 判别核心:days 模式不能带 until(后端 omitempty 二义)。变异(同时塞 until)→ RED。
    expect('until' in r.request).toBe(false)
  })
  it('days 非正整数 → 失败', () => {
    expect('error' in buildExtendRequest('days', 1, '0', '', now)).toBe(true)
    expect('error' in buildExtendRequest('days', 1, '-5', '', now)).toBe(true)
    expect('error' in buildExtendRequest('days', 1, '1.5', '', now)).toBe(true)
  })
  it('until 模式:未来时间 → ISO 串,仅下发 until', () => {
    const r = buildExtendRequest('until', 1, '', '2026-12-31T00:00:00Z', now)
    expect('request' in r).toBe(true)
    if (!('request' in r)) return
    expect(r.request.until).toBe('2026-12-31T00:00:00.000Z')
    expect('days' in r.request).toBe(false)
  })
  it('until 过去/非法 → 失败', () => {
    // 判别核心:必须拒绝 <= now 的时间(否则把订阅缩短/无效延长)。变异(去掉 ts<=now 判断)→ RED。
    expect('error' in buildExtendRequest('until', 1, '', '2020-01-01T00:00:00Z', now)).toBe(true)
    expect('error' in buildExtendRequest('until', 1, '', 'not-a-date', now)).toBe(true)
    expect('error' in buildExtendRequest('until', 1, '', '', now)).toBe(true)
  })
})

describe('buildVoucherRequest', () => {
  function vform(over: Partial<VoucherFormState>): VoucherFormState {
    return {
      ...EMPTY_VOUCHER_FORM,
      planId: '5',
      amountUsd: '19.99',
      validFrom: '2026-06-29T00:00:00Z',
      validUntil: '2026-12-31T00:00:00Z',
      ...over,
    }
  }
  it('合法表单 → 请求体(名义价换分 + ISO 时间)', () => {
    const r = buildVoucherRequest(vform({}), 3)
    expect('request' in r).toBe(true)
    if (!('request' in r)) return
    expect(r.request.tenant_id).toBe(3)
    expect(r.request.plan_id).toBe(5)
    // 判别核心:名义价 19.99 美元 → 1999 分。变异(去掉 *100)→ RED。
    expect(r.request.amount_cents).toBe(1999)
    expect(r.request.valid_from).toBe('2026-06-29T00:00:00.000Z')
    expect(r.request.single_use_per_user).toBe(true)
  })
  it('plan_id 必须正整数', () => {
    expect('error' in buildVoucherRequest(vform({ planId: '0' }), 1)).toBe(true)
    expect('error' in buildVoucherRequest(vform({ planId: 'abc' }), 1)).toBe(true)
  })
  it('until 必须晚于 from', () => {
    // 判别核心:窗口倒置必须拒。变异(去掉 until<=from 判断)→ RED。
    const r = buildVoucherRequest(
      vform({ validFrom: '2026-12-31T00:00:00Z', validUntil: '2026-06-29T00:00:00Z' }),
      1,
    )
    expect('error' in r).toBe(true)
  })
  it('max_redemptions / eligible_user_id 空串省略、填了校验正整数', () => {
    const ok = buildVoucherRequest(vform({}), 1)
    expect('request' in ok).toBe(true)
    if (!('request' in ok)) return
    // 判别核心:空串不下发(对齐后端 omitempty)。变异(无条件赋值 NaN)→ RED。
    expect('max_redemptions' in ok.request).toBe(false)
    expect('eligible_user_id' in ok.request).toBe(false)
    expect('error' in buildVoucherRequest(vform({ maxRedemptions: '0' }), 1)).toBe(true)
    expect('error' in buildVoucherRequest(vform({ eligibleUserId: '-1' }), 1)).toBe(true)
  })
})

describe('auditEventLabel', () => {
  it('已知事件类型映射为中文(非恒等返回)', () => {
    // 判别核心:已知键必须翻译成对应中文,而不是把原始 event_type 直接吐回。
    // 变异(default 直接返回原串、删掉某 case)→ 对应断言 RED;
    // 同时断言「翻译结果 ≠ 原始串」可在整段 switch 被改成恒等返回时整体打红。
    expect(auditEventLabel('subscription_created')).toBe('订阅创建')
    expect(auditEventLabel('subscription_extended')).toBe('有效期延长')
    expect(auditEventLabel('subscription_revoked')).toBe('订阅撤销')
    expect(auditEventLabel('cancelled')).toBe('已取消')
    expect(auditEventLabel('group_downgraded')).toBe('用户组降级')
    expect(auditEventLabel('idempotent_replay')).toBe('幂等重放')
    // 已知键的翻译必须与原始字面值不同(防 switch 被整体改成 return eventType)。
    expect(auditEventLabel('subscription_created')).not.toBe('subscription_created')
  })
  it('未知事件类型回退原始串(不吞)', () => {
    // 判别核心:未识别的新事件不能被吞成空串/固定占位,必须原样透出便于排查。
    // 变异(default 返回 '—' 或 '')→ 此断言 RED。
    expect(auditEventLabel('some_new_event')).toBe('some_new_event')
  })
})

describe('actorLabel', () => {
  it('actor_kind 映射中文 + 正 actor_id 拼接', () => {
    // 判别核心:kind 翻译为中文,且 actor_id>0 才追加「#id」。
    // 变异(去掉 kind 翻译)→ 第 1/2 条 RED;变异(无条件拼 id)→ 系统那条 RED。
    expect(actorLabel('admin', 42)).toBe('管理员 #42')
    expect(actorLabel('user', 7)).toBe('用户 #7')
  })
  it('actor_id 缺省 / 为 0 不拼「#id」', () => {
    // 判别核心:系统事件常无 actor_id,拼「#0」是误导。
    // 变异(把 >0 写成 >=0 或去掉判断)→ 这两条 RED。
    expect(actorLabel('system')).toBe('系统')
    expect(actorLabel('system', 0)).toBe('系统')
  })
  it('未知 kind 回退原串 / 空串回退占位', () => {
    // 判别核心:未识别 kind 透出原值;完全空 kind 用「—」占位,避免空白。
    expect(actorLabel('robot', 3)).toBe('robot #3')
    expect(actorLabel('')).toBe('—')
  })
})
