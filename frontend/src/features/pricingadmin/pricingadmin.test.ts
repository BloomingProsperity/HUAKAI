import { describe, expect, it } from 'vitest'
import {
  billingPolicyLabel,
  buildBillingSettingsUpdate,
  parsePositiveDecimal,
  RATIO_MAX_DEFAULT,
  RATIO_MIN,
  scopeLabel,
  validateCacheQualifier,
  validateMultiplier,
  validateRatio,
  validateTenantId,
} from './pricingadmin'

describe('parsePositiveDecimal', () => {
  it('拒绝空 / 非数字 / 科学计数 / 负数 / 零', () => {
    expect('error' in parsePositiveDecimal('')).toBe(true)
    expect('error' in parsePositiveDecimal('abc')).toBe(true)
    expect('error' in parsePositiveDecimal('1e3')).toBe(true)
    expect('error' in parsePositiveDecimal('-1')).toBe(true)
    expect('error' in parsePositiveDecimal('0')).toBe(true)
  })

  it('接受正小数,去前后空白', () => {
    expect(parsePositiveDecimal(' 1.5 ')).toEqual({ value: 1.5 })
    expect(parsePositiveDecimal('100')).toEqual({ value: 100 })
  })
})

describe('validateRatio', () => {
  it('边界 [0.01, 100] 闭区间内通过,越界报错', () => {
    // 判别核心:范围闭区间。变异(改成开区间 / 去掉范围检查)→ 边界或越界断言 RED。
    expect(validateRatio(String(RATIO_MIN))).toEqual({ value: '0.01' })
    expect(validateRatio(String(RATIO_MAX_DEFAULT))).toEqual({ value: '100' })
    expect('error' in validateRatio('0.009')).toBe(true)
    expect('error' in validateRatio('100.1')).toBe(true)
  })

  it('保留原始字符串精度(不走 Number 往返)', () => {
    // 判别核心:回传 trim 后原串而非 String(Number())。变异(返回 String(parsed.value))→ RED。
    expect(validateRatio(' 1.230 ')).toEqual({ value: '1.230' })
  })

  it('可传更高上限(对齐后端 HUAKAI_PRICING_RATIO_MAX 可调)', () => {
    expect(validateRatio('150', 200)).toEqual({ value: '150' })
    expect('error' in validateRatio('150')).toBe(true) // 默认上限 100 时越界
  })
})

describe('validateMultiplier', () => {
  it('正小数通过,无上限', () => {
    expect(validateMultiplier('2.5')).toEqual({ value: '2.5' })
    expect(validateMultiplier('999')).toEqual({ value: '999' })
    expect('error' in validateMultiplier('0')).toBe(true)
  })
})

describe('validateCacheQualifier', () => {
  it('global 无需限定值', () => {
    expect(validateCacheQualifier('global', {})).toEqual({})
  })

  it('model 需非空模型名', () => {
    // 判别核心:model scope 必须带 model。变异(global 分支兜底返回 {})→ 空 model 不报错 → RED。
    expect('error' in validateCacheQualifier('model', { model: '' })).toBe(true)
    expect(validateCacheQualifier('model', { model: ' gpt ' })).toEqual({ model: 'gpt' })
  })

  it('tenant 需正整数 id', () => {
    expect('error' in validateCacheQualifier('tenant', { tenantId: '0' })).toBe(true)
    expect('error' in validateCacheQualifier('tenant', { tenantId: 'x' })).toBe(true)
    expect(validateCacheQualifier('tenant', { tenantId: '7' })).toEqual({ tenantId: 7 })
  })
})

describe('validateTenantId', () => {
  it('正整数通过,非法报错', () => {
    expect(validateTenantId(' 3 ')).toEqual({ value: 3 })
    expect('error' in validateTenantId('0')).toBe(true)
    expect('error' in validateTenantId('1.5')).toBe(true)
  })
})

describe('scopeLabel', () => {
  it('已知 scope 映射中文,未知透传', () => {
    expect(scopeLabel('global')).toBe('全局')
    expect(scopeLabel('model')).toBe('按模型')
    expect(scopeLabel('tenant')).toBe('按租户')
    expect(scopeLabel('weird')).toBe('weird')
  })
})

describe('billingPolicyLabel', () => {
  it('已知策略值映射中文,未知透传', () => {
    expect(billingPolicyLabel('no_bill')).toContain('不结算')
    expect(billingPolicyLabel('no_bill_record')).toContain('记录')
    expect(billingPolicyLabel('bill_input')).toContain('路线图')
    expect(billingPolicyLabel('weird')).toBe('weird')
  })
})

describe('buildBillingSettingsUpdate', () => {
  const allowed = ['no_bill', 'no_bill_record']

  it('合法策略 + reason → 请求体(reason trim)', () => {
    const r = buildBillingSettingsUpdate(1, 'no_bill_record', '  合规切换  ', allowed)
    expect('request' in r).toBe(true)
    if (!('request' in r)) return
    expect(r.request.tenant_id).toBe(1)
    expect(r.request.stream_input_only_interrupted_policy).toBe('no_bill_record')
    expect(r.request.reason).toBe('合规切换')
  })

  it('reason 空 → 失败', () => {
    // 判别核心:reason 必填(后端 billing_settings_reason_required)。变异(去 reason 判断)→ RED。
    expect('error' in buildBillingSettingsUpdate(1, 'no_bill', '', allowed)).toBe(true)
    expect('error' in buildBillingSettingsUpdate(1, 'no_bill', '   ', allowed)).toBe(true)
  })

  it('路线图值 bill_input 必须拦截(且给路线图专属错误,而非泛化非法)', () => {
    // 判别核心:bill_input 永不下发,且要给「路线图值」专属提示(后端会回 409 billing_policy_value_roadmap)。
    // 即使把 bill_input 放进 allowed,roadmap 守卫也必须先拦截。
    // 变异(去掉 roadmap 守卫)→ 含 bill_input 的 allowed 下会放行 → 此断言 RED。
    const blocked = buildBillingSettingsUpdate(1, 'bill_input', '试试', ['no_bill', 'bill_input'])
    expect('error' in blocked).toBe(true)
    if (!('error' in blocked)) return
    expect(blocked.error).toContain('路线图')
  })

  it('不在 allowed 列表 → 失败', () => {
    // 判别核心:策略值必须落在后端回的 allowed_values 内。变异(去掉 includes 判断)→ RED。
    expect('error' in buildBillingSettingsUpdate(1, 'random_value', '试试', allowed)).toBe(true)
    // allowed 收窄时,原本合法的值也应被拦截(证明确实读 allowed 而非硬编码)。
    expect('error' in buildBillingSettingsUpdate(1, 'no_bill_record', '试试', ['no_bill'])).toBe(true)
  })
})
