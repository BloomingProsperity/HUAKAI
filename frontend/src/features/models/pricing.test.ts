import { describe, expect, it } from 'vitest'
import { capabilityList, filterModels, groupByOwner, pricePerMillion } from './pricing'
import type { PricingItem } from './types'

function item(over: Partial<PricingItem>): PricingItem {
  return { model: 'm', ...over }
}

describe('pricePerMillion', () => {
  it('每-token 价换算成每-百万-token($)', () => {
    // 判别核心:必须 ×1e6。变异(去掉 *1_000_000)→ "0.000003" 会显示成 $0.00 而非 $3.00→RED。
    expect(pricePerMillion('0.000003')).toBe('$3.00')
    expect(pricePerMillion('0.000015')).toBe('$15.00')
  })

  it('极小值用 4 位小数避免显示 $0.00', () => {
    expect(pricePerMillion('0.000000005')).toBe('$0.0050')
  })

  it('空/非法/负 → —', () => {
    expect(pricePerMillion(undefined)).toBe('—')
    expect(pricePerMillion('abc')).toBe('—')
    expect(pricePerMillion('-1')).toBe('—')
  })
})

describe('capabilityList', () => {
  it('只取值为 true 的能力键', () => {
    expect(capabilityList({ vision: true, tools: false, json: true }).sort()).toEqual(['json', 'vision'])
    expect(capabilityList(undefined)).toEqual([])
  })
})

describe('filterModels', () => {
  const items = [
    item({ model: 'claude-x', owned_by: 'anthropic' }),
    item({ model: 'gpt-y', owned_by: 'openai' }),
  ]
  it('按名/厂商大小写不敏感过滤,空查询返回原集', () => {
    expect(filterModels(items, 'CLAUDE').map((i) => i.model)).toEqual(['claude-x'])
    expect(filterModels(items, 'openai').map((i) => i.model)).toEqual(['gpt-y'])
    expect(filterModels(items, '')).toHaveLength(2)
  })
})

describe('groupByOwner', () => {
  it('按厂商分组保持原序,无 owned_by 归其他', () => {
    const g = groupByOwner([
      item({ model: 'a', owned_by: 'x' }),
      item({ model: 'b' }),
      item({ model: 'c', owned_by: 'x' }),
    ])
    expect(g.map((x) => x.owner)).toEqual(['x', '其他'])
    expect(g[0].models.map((m) => m.model)).toEqual(['a', 'c'])
  })
})
