import { describe, expect, it } from 'vitest'
import {
  batchStatusLabel,
  buildBatchRequest,
  buildCreateRequest,
  centsToYuan,
  EMPTY_BATCH_FORM,
  EMPTY_CREATE_FORM,
  filterByStatus,
  grantKindLabel,
  MAX_BATCH_COUNT,
  parseListTenantId,
  statusLabel,
  statusTone,
  toIso,
  yuanToCents,
  type BatchForm,
  type CreateForm,
} from './vouchersadmin'

function cForm(over: Partial<CreateForm>): CreateForm {
  // 默认填齐有效期窗口,便于聚焦单字段校验。
  return { ...EMPTY_CREATE_FORM, validFrom: '2026-01-01T00:00', validUntil: '2026-12-31T00:00', amountYuan: '10', ...over }
}
function bForm(over: Partial<BatchForm>): BatchForm {
  return { ...EMPTY_BATCH_FORM, validFrom: '2026-01-01T00:00', validUntil: '2026-12-31T00:00', amountYuan: '10', ...over }
}

describe('yuanToCents / centsToYuan', () => {
  it('元分换算四舍五入到分', () => {
    // 判别核心:1 元 = 100 分。变异(改成 *10)→ RED。
    expect(yuanToCents(1)).toBe(100)
    expect(yuanToCents(12.34)).toBe(1234)
    expect(yuanToCents(0.005)).toBe(1) // 四舍五入到 1 分
    expect(centsToYuan(1234)).toBe('12.34')
    expect(centsToYuan(100)).toBe('1.00')
  })
})

describe('buildCreateRequest', () => {
  it('租户/面额/有效期/次数 各自校验报错', () => {
    expect(buildCreateRequest(cForm({ tenantId: '0' }))).toEqual({ error: '租户 ID 必须为正整数' })
    expect(buildCreateRequest(cForm({ amountYuan: '0' }))).toEqual({ error: '面额必须为正数' })
    expect(buildCreateRequest(cForm({ validFrom: '', validUntil: '' }))).toEqual({ error: '请填写完整且合法的有效期起止时间' })
    expect(buildCreateRequest(cForm({ maxRedemptions: 'x' }))).toEqual({ error: '最大兑换次数必须为正整数' })
  })

  it('生效时间不早于失效时间 → 报错', () => {
    // 判别核心:from 必须严格早于 until。变异(去掉时序校验)→ 通过 → RED。
    expect(buildCreateRequest(cForm({ validFrom: '2026-12-31T00:00', validUntil: '2026-01-01T00:00' }))).toEqual({
      error: '生效时间必须早于失效时间',
    })
  })

  it('齐全 → 正确请求,空 code 省略且面额转分', () => {
    const r = buildCreateRequest(cForm({ tenantId: '7', amountYuan: '25.5', currencyCode: 'USD', code: '' }))
    expect(r).toMatchObject({ tenant_id: 7, amount_cents: 2550, currency_code: 'USD', max_redemptions: 1, single_use_per_user: true })
    expect('code' in (r as object)).toBe(false)
  })

  it('自定义 code 透传', () => {
    const r = buildCreateRequest(cForm({ code: ' WELCOME10 ' }))
    expect((r as { code?: string }).code).toBe('WELCOME10')
  })
})

describe('buildBatchRequest', () => {
  it('数量超上限报错(后端硬上限 1000)', () => {
    // 判别核心:count 必须 ≤ MAX_BATCH_COUNT。变异(去掉上限)→ 1001 通过 → RED。
    expect(buildBatchRequest(bForm({ count: String(MAX_BATCH_COUNT + 1) }))).toEqual({ error: `单批最多生成 ${MAX_BATCH_COUNT} 张` })
    // 边界:恰好 1000 应通过。
    const ok = buildBatchRequest(bForm({ count: String(MAX_BATCH_COUNT) }))
    expect((ok as { count?: number }).count).toBe(MAX_BATCH_COUNT)
  })

  it('数量非正整数报错', () => {
    expect(buildBatchRequest(bForm({ count: '0' }))).toEqual({ error: '生成数量必须为正整数' })
    expect(buildBatchRequest(bForm({ count: '3.5' }))).toEqual({ error: '生成数量必须为正整数' })
  })

  it('齐全 → 正确批量请求', () => {
    const r = buildBatchRequest(bForm({ tenantId: '2', count: '50', amountYuan: '5' }))
    expect(r).toMatchObject({ tenant_id: 2, count: 50, amount_cents: 500, currency_code: 'USD' })
  })
})

describe('parseListTenantId', () => {
  it('正整数通过,其余 null(后端列表 tenant_id 必填正整数)', () => {
    expect(parseListTenantId('3')).toBe(3)
    expect(parseListTenantId('0')).toBeNull()
    expect(parseListTenantId('-1')).toBeNull()
    expect(parseListTenantId('abc')).toBeNull()
  })
})

describe('statusTone / statusLabel', () => {
  it('状态映射配色与标签', () => {
    // 判别核心:revoked 必须危险色。变异(返回 muted)→ RED。
    expect(statusTone('revoked')).toBe('danger')
    expect(statusTone('active')).toBe('ok')
    expect(statusTone('expired')).toBe('warn')
    expect(statusTone('exhausted')).toBe('warn')
    expect(statusLabel('active')).toBe('可用')
    expect(statusLabel('revoked')).toBe('已吊销')
  })
})

describe('batchStatusLabel / grantKindLabel', () => {
  it('批次状态与授予种类标签', () => {
    expect(batchStatusLabel('completed')).toBe('已完成')
    expect(grantKindLabel('subscription')).toBe('订阅')
    expect(grantKindLabel('')).toBe('余额') // 空 grant_kind 默认按余额展示
  })
})

describe('toIso', () => {
  it('合法 datetime-local → ISO,空/非法 → 空串', () => {
    expect(toIso('')).toBe('')
    expect(toIso('not-a-date')).toBe('')
    expect(toIso('2026-06-01T12:00')).not.toBe('')
  })
})

describe('filterByStatus', () => {
  it('空状态返回全部,指定状态只留匹配', () => {
    const rows = [{ status: 'active' }, { status: 'revoked' }, { status: 'active' }]
    expect(filterByStatus(rows, '')).toHaveLength(3)
    expect(filterByStatus(rows, 'active')).toHaveLength(2)
    expect(filterByStatus(rows, 'revoked')).toHaveLength(1)
  })
})
