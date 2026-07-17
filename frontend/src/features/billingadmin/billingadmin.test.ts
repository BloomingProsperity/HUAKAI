import { describe, expect, it, vi } from 'vitest'
import {
  buildClaimQuery,
  buildRepriceRequest,
  buildUsageQuery,
  canStartReprice,
  claimStatusTone,
  executeRepriceGuarded,
  formatMoney,
  formatTime,
  mapClaimTableRows,
  mapRepriceTableRows,
  mapUsageTableRows,
  shortId,
  sumRepriceCostDelta,
  toIso,
  trustStatusTone,
  validateRepriceForm,
} from './billingadmin'
import {
  EMPTY_CLAIM_FILTERS,
  EMPTY_USAGE_FILTERS,
  type ClaimFilters,
  type RepriceForm,
  type RepriceRequest,
  type BillingClaim,
  type UsageRecord,
  type UsageFilters,
} from './types'

function uf(over: Partial<UsageFilters>): UsageFilters {
  return { ...EMPTY_USAGE_FILTERS, ...over }
}
function cf(over: Partial<ClaimFilters>): ClaimFilters {
  return { ...EMPTY_CLAIM_FILTERS, ...over }
}

function rf(over: Partial<RepriceForm> = {}): RepriceForm {
  return {
    scope: 'record',
    usageRecordId: '41',
    tenantId: '',
    from: '',
    to: '',
    limit: '100',
    reason: '修复历史待对账记录',
    acknowledged: true,
    ...over,
  }
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

describe('计费列表列映射', () => {
  it('用量列锁定模型、Token、money 与待对账语义', () => {
    const source = {
      id: 9,
      created_at: '2026-07-13T00:00:00Z',
      requested_model: 'claude-a',
      upstream_model: 'claude-b',
      provider: 'anthropic',
      tokens_input: 12,
      tokens_output: 34,
      actual_cost: '0.01000000',
      trust_status: 'verified',
      pending_reconciliation: true,
      request_id: 'req_0123456789abcdef0123',
    } as UsageRecord
    const [row] = mapUsageTableRows([source])
    // 变异(输入输出 Token 漏一项或 money 转 Number)会使精确列值不相等而 RED。
    expect(row).toMatchObject({ id: 9, requestedModel: 'claude-a', upstreamModel: 'claude-b', tokens: '12 / 34', actualCost: '0.01000000', pendingReconciliation: true })
    expect(row.source).toBe(source)
  })

  it('Claim 列严格区分预扣、实际成本与结算时间', () => {
    const claim = {
      id: 4,
      created_at: '2026-07-13T00:00:00Z',
      requested_model: 'gpt-x',
      endpoint_family: 'responses',
      status: 'settled',
      predicted_cost: '1.2300',
      actual_cost: '1.1000',
      currency_code: 'USD',
      settled_at: '2026-07-13T00:01:00Z',
      logical_request_id: 'logical-1',
    } as BillingClaim
    const [row] = mapClaimTableRows([claim])
    // 变异(预扣/实际列互换)会直接打红。
    expect(row.predictedCost).toBe('1.2300 USD')
    expect(row.actualCost).toBe('1.1000 USD')
    expect(row.status).toBe('settled')
  })

  it('重算结果列保留原始金额并按错误优先展示原因', () => {
    const [row] = mapRepriceTableRows([{
      usage_record_id: 8, tenant_id: 3, status: 'error', original_cost: '0.10000000',
      authoritative_cost: '0.20000000', cost_delta: '0.10000000', error_message: '价表缺失', pricing_source: 'fallback',
    }])
    expect(row).toEqual({ id: '3-8', usageRecordId: '8', tenantId: '3', status: 'error', originalCost: '0.10000000', authoritativeCost: '0.20000000', costDelta: '0.10000000', detail: '价表缺失' })
  })
})

describe('计费重算范围与请求体', () => {
  it('单条范围只下发 usage_record_id + dry_run，不伪造原因/幂等字段', () => {
    const request = buildRepriceRequest(rf(), false)
    expect(request).toEqual({ usage_record_id: 41, dry_run: false })
    expect('reason' in request).toBe(false)
    expect('idempotency_key' in request).toBe(false)
  })

  it('时间窗范围转 RFC3339，严格锁定 tenant/from/to/limit', () => {
    const request = buildRepriceRequest(
      rf({
        scope: 'window',
        tenantId: '7',
        from: '2026-07-10T00:00',
        to: '2026-07-11T00:00',
        limit: '42',
      }),
      true,
    )
    expect(request).toMatchObject({ tenant_id: 7, limit: 42, dry_run: true })
    expect('usage_record_id' in request).toBe(false)
    if (!('from' in request)) throw new Error('期望时间窗重算请求')
    expect(Date.parse(request.from)).toBeLessThan(Date.parse(request.to))
  })

  it('原因必填、时间正序、limit≤100；合法表单才可开始', () => {
    expect(validateRepriceForm(rf({ reason: '  ' }))).toContain('原因')
    expect(validateRepriceForm(rf({ scope: 'window', tenantId: '7', from: '2026-07-11T00:00', to: '2026-07-10T00:00' }))).toContain('早于')
    expect(validateRepriceForm(rf({ scope: 'window', tenantId: '7', from: '2026-07-10T00:00', to: '2026-07-11T00:00', limit: '101' }))).toContain('100')
    expect(canStartReprice(rf({ acknowledged: false }))).toBe(false)
    expect(canStartReprice(rf())).toBe(true)
  })
})

describe('计费重算危险闸门', () => {
  it('未勾选知情确认时不发请求', async () => {
    const send = vi.fn(async () => ({ ok: true }))
    await expect(executeRepriceGuarded(rf({ acknowledged: false }), 'apply', true, send)).rejects.toThrow('勾选')
    expect(send).not.toHaveBeenCalled()
  })

  it('已勾选但未完成二次确认时仍不发实际重算', async () => {
    const send = vi.fn(async () => ({ ok: true }))
    await expect(executeRepriceGuarded(rf(), 'apply', false, send)).rejects.toThrow('二次确认')
    expect(send).not.toHaveBeenCalled()
  })

  it('预演不要求危险确认但仍要求勾选；实际重算确认后才发 dry_run=false', async () => {
    const send = vi.fn(async (request: RepriceRequest) => request)
    await executeRepriceGuarded(rf(), 'preview', false, send)
    await executeRepriceGuarded(rf(), 'apply', true, send)
    expect(send).toHaveBeenNthCalledWith(1, { usage_record_id: 41, dry_run: true })
    expect(send).toHaveBeenNthCalledWith(2, { usage_record_id: 41, dry_run: false })
  })
})

describe('重算差额定点汇总', () => {
  it('正负八位小数精确求和，不经过 Number', () => {
    expect(sumRepriceCostDelta([
      { cost_delta: '0.10000001' },
      { cost_delta: '-0.02000000' },
      { cost_delta: '0.00000009' },
    ])).toBe('0.08000010')
    expect(sumRepriceCostDelta([{ cost_delta: '9007199254740993.00000001' }])).toBe('9007199254740993.00000001')
  })

  it('响应差额非法时返回 null，不伪造金额', () => {
    expect(sumRepriceCostDelta([{ cost_delta: 'not-money' }])).toBeNull()
  })
})
