import { describe, expect, it } from 'vitest'
import {
  buildChannels,
  capabilityList,
  filterCatalog,
  formatPrice,
  formatPriceRange,
  OTHER_CHANNEL,
  perMillion,
} from './channels'
import type { PricingItem } from './types'

const item = (over: Partial<PricingItem>): PricingItem => ({ model: 'm', ...over })

describe('perMillion', () => {
  it('把每-token 极小数换算成每-百万-token', () => {
    // 判别核心:×1e6 换算正确。变异(改成 1e3 / 不乘)即被此断言抓住。
    expect(perMillion('0.000003')).toBe(3)
  })
  it('空 / 非法 / 负 → null', () => {
    expect(perMillion(undefined)).toBeNull()
    expect(perMillion('abc')).toBeNull()
    expect(perMillion('-0.001')).toBeNull()
  })
})

describe('formatPrice', () => {
  it('mtok 单位:每百万 token 两位小数', () => {
    expect(formatPrice('0.000003', 'mtok')).toBe('$3.00')
  })
  it('token 单位:原始极小数去尾零', () => {
    expect(formatPrice('0.000003', 'token')).toBe('$0.000003')
  })
  it('缺价 → 破折号', () => {
    expect(formatPrice(undefined, 'mtok')).toBe('—')
  })
})

describe('capabilityList', () => {
  it('只取值为 true 的能力键', () => {
    expect(capabilityList({ vision: true, tools: false, json: true })).toEqual(['vision', 'json'])
  })
})

describe('filterCatalog', () => {
  const items = [
    item({ model: 'claude-x', owned_by: 'anthropic' }),
    item({ model: 'gpt-y', owned_by: 'openai', canonical_id: 'gpt-y-2024' }),
  ]
  it('按厂商大小写不敏感命中', () => {
    expect(filterCatalog(items, 'OPENAI').map((i) => i.model)).toEqual(['gpt-y'])
  })
  it('空查询原样返回', () => {
    expect(filterCatalog(items, '  ')).toBe(items)
  })
})

describe('buildChannels', () => {
  const items = [
    item({ model: 'a1', owned_by: 'anthropic', output_price_per_token: '0.000015', capabilities: { vision: true } }),
    item({ model: 'a2', owned_by: 'anthropic', output_price_per_token: '0.000003', capabilities: { tools: true } }),
    item({ model: 'o1', owned_by: 'openai', output_price_per_token: '0.00001' }),
    item({ model: 'x1' }), // 无 owned_by → 「其他」
  ]

  it('按厂商聚合成渠道,保持首次出现序', () => {
    const ch = buildChannels(items)
    // 判别核心:分组数与顺序。变异(去重逻辑错/顺序乱)即被抓。
    expect(ch.map((c) => c.name)).toEqual(['anthropic', 'openai', OTHER_CHANNEL])
  })

  it('每渠道模型数正确', () => {
    const anthropic = buildChannels(items).find((c) => c.name === 'anthropic')!
    expect(anthropic.modelCount).toBe(2)
  })

  it('输出价区间取该渠道最低/最高(每百万 token)', () => {
    const anthropic = buildChannels(items).find((c) => c.name === 'anthropic')!
    // 判别核心:min=3、max=15。变异(min/max 取反或只取首个)即被抓。
    expect(anthropic.outputPriceRange).toEqual({ min: 3, max: 15 })
  })

  it('能力做并集去重排序', () => {
    const anthropic = buildChannels(items).find((c) => c.name === 'anthropic')!
    expect(anthropic.capabilities).toEqual(['tools', 'vision'])
  })

  it('无任何有效价 → 区间为 null', () => {
    const other = buildChannels(items).find((c) => c.name === OTHER_CHANNEL)!
    expect(other.outputPriceRange).toBeNull()
  })
})

describe('formatPriceRange', () => {
  it('min 等于 max 时只显示单值', () => {
    expect(formatPriceRange({ min: 3, max: 3 })).toBe('$3.00')
  })
  it('min 不等 max 时显示区间', () => {
    // 判别核心:区间格式。变异(总是显示单值)即被抓。
    expect(formatPriceRange({ min: 3, max: 15 })).toBe('$3.00 – $15.00')
  })
  it('null → 破折号', () => {
    expect(formatPriceRange(null)).toBe('—')
  })
})
