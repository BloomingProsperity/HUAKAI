import { describe, expect, it } from 'vitest'
import {
  applyFilters,
  capabilityList,
  collectCapabilities,
  collectModes,
  collectOwners,
  filterModels,
  formatPrice,
  formatScaled,
  groupByOwner,
  pricePerMillion,
} from './pricing'
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

describe('formatScaled', () => {
  it('去尾零并修浮点', () => {
    // 判别核心:必须去掉尾零。变异(不 strip)→ "0.00000300" ≠ "0.000003" → RED。
    expect(formatScaled(0.000003, 8)).toBe('0.000003')
    expect(formatScaled(3, 2)).toBe('3')
    expect(formatScaled(3.5, 2)).toBe('3.5')
  })
  it('非有限 → —', () => {
    expect(formatScaled(NaN, 2)).toBe('—')
  })
})

describe('formatPrice', () => {
  it('mtok 单位 ×1e6 显示每百万 token 价', () => {
    // 判别核心:mtok 必须 ×1e6。变异(去 *1e6)→ "0.000003" 显示 $0 而非 $3.00 → RED。
    expect(formatPrice('0.000003', 'mtok')).toBe('$3.00')
    expect(formatPrice('0.000000005', 'mtok')).toBe('$0.0050')
  })
  it('token 单位显示原始每 token 价(去尾零)', () => {
    // 判别核心:token 单位绝不能 ×1e6。变异(token 也 ×1e6)→ 得 $3 而非 $0.000003 → RED。
    expect(formatPrice('0.000003', 'token')).toBe('$0.000003')
  })
  it('空/非法/负 → —;零 → $0', () => {
    expect(formatPrice(undefined, 'mtok')).toBe('—')
    expect(formatPrice('-1', 'token')).toBe('—')
    expect(formatPrice('0', 'mtok')).toBe('$0')
  })
})

describe('facet 收集', () => {
  const items = [
    item({ model: 'a', owned_by: 'openai', mode: 'chat', capabilities: { vision: true, tools: false } }),
    item({ model: 'b', owned_by: 'anthropic', mode: 'chat', capabilities: { tools: true } }),
    item({ model: 'c', mode: undefined, capabilities: { vision: true } }),
  ]
  it('collectOwners 去重排序,无 owned_by 归其他', () => {
    expect(collectOwners(items)).toEqual(['anthropic', 'openai', '其他'])
  })
  it('collectModes 去重排序,忽略空 mode', () => {
    expect(collectModes(items)).toEqual(['chat'])
  })
  it('collectCapabilities 仅取 true 的能力,去重排序', () => {
    expect(collectCapabilities(items)).toEqual(['tools', 'vision'])
  })
})

describe('applyFilters', () => {
  const items = [
    item({ model: 'claude-opus', owned_by: 'anthropic', mode: 'chat', capabilities: { vision: true, tools: true } }),
    item({ model: 'gpt-4o', owned_by: 'openai', mode: 'chat', capabilities: { tools: true } }),
    item({ model: 'gemini', owned_by: 'google', mode: 'embedding', capabilities: { vision: true } }),
  ]
  it('厂商维精确过滤', () => {
    expect(applyFilters(items, { query: '', owner: 'openai', mode: '', capability: '' }).map((i) => i.model)).toEqual(['gpt-4o'])
  })
  it('模式维精确过滤', () => {
    expect(applyFilters(items, { query: '', owner: '', mode: 'embedding', capability: '' }).map((i) => i.model)).toEqual(['gemini'])
  })
  it('能力维要求模型具备该能力', () => {
    // 判别核心:capability 维必须真过滤。变异(忽略 capability)→ 返回全部 3 个而非只 vision 的 2 个 → RED。
    expect(applyFilters(items, { query: '', owner: '', mode: '', capability: 'vision' }).map((i) => i.model)).toEqual(['claude-opus', 'gemini'])
  })
  it('多维叠加(文本 + 能力)', () => {
    expect(applyFilters(items, { query: 'gpt', owner: '', mode: '', capability: 'tools' }).map((i) => i.model)).toEqual(['gpt-4o'])
  })
  it('全空筛选返回原集', () => {
    expect(applyFilters(items, { query: '', owner: '', mode: '', capability: '' })).toHaveLength(3)
  })
})
