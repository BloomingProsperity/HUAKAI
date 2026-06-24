import { describe, expect, it } from 'vitest'
import { buildBatchRevoke, isSelectable, summarizeBatchResult, toggleSelected } from './batch'
import type { ApiKeyView } from './types'

describe('isSelectable', () => {
  it('仅活跃 Key 可选', () => {
    // 判别核心:非 active 不可选。变异(恒 true)→ 已撤销 Key 也可选,本断言 RED。
    expect(isSelectable({ status: 'active' } as ApiKeyView)).toBe(true)
    expect(isSelectable({ status: 'revoked' } as ApiKeyView)).toBe(false)
    expect(isSelectable({ status: 'expired' } as ApiKeyView)).toBe(false)
  })
})

describe('toggleSelected', () => {
  it('切换增删,返回新 Set 不改原集', () => {
    const a = new Set<number>([1])
    const b = toggleSelected(a, 2)
    expect([...b].sort()).toEqual([1, 2])
    expect([...toggleSelected(b, 1)]).toEqual([2])
    expect([...a]).toEqual([1]) // 原集不变
  })
})

describe('buildBatchRevoke', () => {
  it('空选 → 报错', () => {
    expect(buildBatchRevoke([], 'x')).toEqual({ error: '请先勾选要撤销的 Key' })
  })
  it('超 200 → 报错', () => {
    const ids = Array.from({ length: 201 }, (_, i) => i + 1)
    expect(buildBatchRevoke(ids, '')).toEqual({ error: '单次最多撤销 200 个,请分批操作' })
  })
  it('正常 → {ids,reason},reason 去空白', () => {
    expect(buildBatchRevoke([3, 5], '  清理  ')).toEqual({ ids: [3, 5], reason: '清理' })
  })
})

describe('summarizeBatchResult', () => {
  it('仅撤销数;有未找到时追加提示', () => {
    // 判别核心:not_found 非空必须提示。变异(忽略 not_found)→ 第二断言 RED。
    expect(summarizeBatchResult({ revoked: [1, 2, 3], not_found: [] })).toBe('已撤销 3 个 Key')
    expect(summarizeBatchResult({ revoked: [1], not_found: [9] })).toBe('已撤销 1 个 Key(1 个未找到/已失效)')
  })
})
