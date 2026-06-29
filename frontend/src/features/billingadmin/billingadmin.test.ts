import { describe, expect, it } from 'vitest'
import {
  buildClaimQuery,
  buildUsageQuery,
  claimStatusTone,
  formatMoney,
  formatTime,
  shortId,
  toIso,
  trustStatusTone,
} from './billingadmin'
import { EMPTY_CLAIM_FILTERS, EMPTY_USAGE_FILTERS, type ClaimFilters, type UsageFilters } from './types'

function uf(over: Partial<UsageFilters>): UsageFilters {
  return { ...EMPTY_USAGE_FILTERS, ...over }
}
function cf(over: Partial<ClaimFilters>): ClaimFilters {
  return { ...EMPTY_CLAIM_FILTERS, ...over }
}

describe('buildUsageQuery', () => {
  it('空过滤 → 仅可能含 cursor;不下发任何空串字段', () => {
    // 判别核心:空过滤必须产出空对象。变异(把 putStr/putPositiveInt 改成无条件赋值)
    // → query 会含 provider:'' 等空串字段 → 本断言 RED。
    expect(buildUsageQuery(EMPTY_USAGE_FILTERS)).toEqual({})
  })

  it('字符串字段 trim 后下发,空白省略', () => {
    const q = buildUsageQuery(uf({ provider: ' anthropic ', model: '' }))
    expect(q).toEqual({ provider: 'anthropic' })
    expect('model' in q).toBe(false)
  })

  it('正整数过滤:合法下发,非正/非数字一律省略(防后端 400)', () => {
    // 判别核心:后端 parseIntFilter 对 <=0 / 非数字直接 400;前端必须先收敛。
    // 变异(去掉 putPositiveInt 的正整数守卫、直接赋值)→ pool_id:'0'/'abc'/'-3' 会被下发 → 本断言 RED。
    const ok = buildUsageQuery(uf({ poolId: '7', apiKeyId: '0', providerAccountId: 'abc' }))
    expect(ok.pool_id).toBe('7')
    expect('api_key_id' in ok).toBe(false) // 0 不是正整数
    expect('provider_account_id' in ok).toBe(false) // 非数字
    const neg = buildUsageQuery(uf({ poolId: '-3' }))
    expect('pool_id' in neg).toBe(false)
  })

  it('outcome 仅 success/error 才下发,all/空/非法省略', () => {
    // 判别核心:后端只接受 success/error/all;空等价 all,故空时不下发更省。
    // 变异(无条件 q.outcome = filters.outcome)→ 空串 outcome 会被下发 → 本断言 RED。
    expect('outcome' in buildUsageQuery(uf({ outcome: '' }))).toBe(false)
    expect('outcome' in buildUsageQuery(uf({ outcome: 'all' }))).toBe(false)
    expect(buildUsageQuery(uf({ outcome: 'success' })).outcome).toBe('success')
    expect(buildUsageQuery(uf({ outcome: 'error' })).outcome).toBe('error')
    expect('outcome' in buildUsageQuery(uf({ outcome: 'bogus' }))).toBe(false)
  })

  it('pendingOnly 仅 true 才下发 pending_reconciliation_only=true', () => {
    // 判别核心:false 时不能下发(否则后端 trim== "true" 判否,但下发空噪声);只在 true 时带。
    // 变异(无条件下发该参数)→ pendingOnly:false 仍会含该键 → 本断言 RED。
    expect('pending_reconciliation_only' in buildUsageQuery(uf({ pendingOnly: false }))).toBe(false)
    expect(buildUsageQuery(uf({ pendingOnly: true })).pending_reconciliation_only).toBe('true')
  })

  it('from/to 转 ISO,cursor 非空才带', () => {
    const q = buildUsageQuery(uf({ from: '2026-06-24T00:00' }), 'cur1')
    expect(typeof q.from).toBe('string')
    expect((q.from as string).includes('2026-06-2')).toBe(true)
    expect(q.cursor).toBe('cur1')
    expect('cursor' in buildUsageQuery(EMPTY_USAGE_FILTERS, '   ')).toBe(false)
  })
})

describe('buildClaimQuery', () => {
  it('status 自由字符串 trim 后下发,空省略', () => {
    expect(buildClaimQuery(cf({ status: ' committed ' })).status).toBe('committed')
    expect('status' in buildClaimQuery(EMPTY_CLAIM_FILTERS)).toBe(false)
  })

  it('claim 无 outcome / pending 维度(即便误置也不下发)', () => {
    // 判别核心:claim 端点没有这两个过滤;buildClaimQuery 绝不应产出它们。
    // 变异(误把 usage 的 outcome/pending 复制进来)→ 出现这两键 → 本断言 RED。
    const q = buildClaimQuery(cf({ status: 'pending', poolId: '5' }))
    expect('outcome' in q).toBe(false)
    expect('pending_reconciliation_only' in q).toBe(false)
    expect(q).toEqual({ status: 'pending', pool_id: '5' })
  })

  it('正整数过滤同样收敛(0/非数字省略)', () => {
    const q = buildClaimQuery(cf({ poolId: '9', apiKeyId: '0', providerAccountId: ' 12 ' }))
    expect(q.pool_id).toBe('9')
    expect('api_key_id' in q).toBe(false)
    expect(q.provider_account_id).toBe('12')
  })
})

describe('toIso', () => {
  it('空串/非法 → 空串', () => {
    expect(toIso('')).toBe('')
    expect(toIso('not-a-date')).toBe('')
  })
})

describe('formatMoney', () => {
  it('原样渲染十进制字符串,绝不做数值转换(防精度丢失)', () => {
    // 判别核心:必须按字符串原样输出,不能 parseFloat 后再 toString(会丢尾零/精度)。
    // 变异(改成 String(parseFloat(value)))→ '0.0000100' 会变 '0.00001' → 本断言 RED。
    expect(formatMoney('0.0000100')).toBe('0.0000100')
    expect(formatMoney('123.456789012345')).toBe('123.456789012345')
  })

  it('拼货币代码;null/空 → 占位符', () => {
    expect(formatMoney('1.50', 'USD')).toBe('1.50 USD')
    expect(formatMoney(null)).toBe('—')
    expect(formatMoney('   ')).toBe('—')
    expect(formatMoney('2.00', '  ')).toBe('2.00') // 空货币不拼
  })
})

describe('claimStatusTone', () => {
  it('settled/committed→ok,pending→info,aborted→danger,其余→muted,大小写不敏感', () => {
    expect(claimStatusTone('settled')).toBe('ok')
    expect(claimStatusTone('COMMITTED')).toBe('ok')
    expect(claimStatusTone('pending')).toBe('info')
    expect(claimStatusTone('aborted')).toBe('danger')
    expect(claimStatusTone('weird')).toBe('muted')
  })
})

describe('trustStatusTone', () => {
  it('verified→ok,mismatch→danger,degraded→warn,missing→muted', () => {
    expect(trustStatusTone('verified')).toBe('ok')
    expect(trustStatusTone('mismatch')).toBe('danger')
    expect(trustStatusTone('degraded')).toBe('warn')
    expect(trustStatusTone('missing')).toBe('muted')
  })
})

describe('formatTime', () => {
  it('null/空/非法 兜底为占位符或原样', () => {
    expect(formatTime(null)).toBe('—')
    expect(formatTime('')).toBe('—')
    expect(formatTime('garbage')).toBe('garbage')
  })
})

describe('shortId', () => {
  it('长串截断保留首尾,短串原样,空 → 占位符', () => {
    expect(shortId('')).toBe('—')
    expect(shortId('abcd')).toBe('abcd')
    const long = 'req_0123456789abcdef0123'
    const out = shortId(long)
    expect(out.startsWith('req_0123')).toBe(true)
    expect(out.endsWith('0123')).toBe(true)
    expect(out.includes('…')).toBe(true)
  })
})
