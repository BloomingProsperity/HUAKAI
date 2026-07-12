import { describe, expect, it } from 'vitest'
import { accountCountLabel, keyCount, metricDisplay, quotaWindowCount } from './metrics'

describe('metricDisplay', () => {
  it('loading→…、unavailable→—、ok→fmt(value)', () => {
    // 判别核心:三态各走各路。变异(unavailable 也走 fmt)→ 第二断言 RED。
    expect(metricDisplay({ status: 'loading' }, String)).toBe('…')
    expect(metricDisplay({ status: 'unavailable' }, String)).toBe('—')
    expect(metricDisplay({ status: 'ok', value: 7 }, (n) => `${n} 个`)).toBe('7 个')
  })
})

describe('accountCountLabel', () => {
  it('有下一页 → N+(不把首页条数误报成总数)', () => {
    // 判别核心:next_cursor 非空必须加 "+"。变异(恒不加+)→ 本断言 RED。
    expect(accountCountLabel({ items: [1, 2, 3], page: { next_cursor: 'abc' } })).toBe('3+')
  })
  it('无下一页 → 纯数字', () => {
    expect(accountCountLabel({ items: [1, 2], page: { next_cursor: null } })).toBe('2')
    expect(accountCountLabel({ items: [] })).toBe('0')
  })
})

describe('keyCount', () => {
  it('优先用后端 count,缺则退回数组长度', () => {
    expect(keyCount({ count: 12, api_keys: [1] })).toBe(12)
    expect(keyCount({ api_keys: [1, 2, 3] })).toBe(3)
    expect(keyCount({})).toBe(0)
  })
})

describe('quotaWindowCount', () => {
  it('配额窗口数', () => {
    expect(quotaWindowCount({ items: [1, 2] })).toBe(2)
    expect(quotaWindowCount({})).toBe(0)
  })
})
