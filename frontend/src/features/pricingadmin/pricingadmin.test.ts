import { describe, expect, it } from 'vitest'
import {
  RATIO_MAX_DEFAULT,
  RATIO_MIN,
  parsePositiveDecimal,
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
