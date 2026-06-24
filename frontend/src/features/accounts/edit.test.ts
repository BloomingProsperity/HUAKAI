import { describe, expect, it } from 'vitest'
import { buildAccountUpdate, formFromAccount, parseTags, type AccountEditForm } from './edit'
import type { ProviderAccount } from './types'

const base = {
  id: 1,
  priority: 10,
  static_weight: 100,
  cap_concurrency: 5,
  tags: ['prod', 'us'],
} as unknown as ProviderAccount

function form(over: Partial<AccountEditForm>): AccountEditForm {
  return { ...formFromAccount(base), ...over }
}

describe('parseTags', () => {
  it('逗号分隔去空白去空项', () => {
    expect(parseTags(' a , b ,, c ')).toEqual(['a', 'b', 'c'])
    expect(parseTags('')).toEqual([])
  })
})

describe('buildAccountUpdate', () => {
  it('只改一个字段 → 体里只含该字段(+reason),不含未改字段', () => {
    // 判别核心:部分更新只发改动项。变异(无脑全量赋值)→ body 会含 static_weight/cap_concurrency→本断言 RED。
    const r = buildAccountUpdate(base, form({ priority: '20', reason: '提权' }))
    expect(r).toEqual({ priority: 20, reason: '提权' })
    expect('static_weight' in (r as object)).toBe(false)
    expect('cap_concurrency' in (r as object)).toBe(false)
    expect('tags' in (r as object)).toBe(false)
  })

  it('标签变更被收录,顺序变化也算变更', () => {
    expect(buildAccountUpdate(base, form({ tags: 'prod, eu' }))).toEqual({ tags: ['prod', 'eu'] })
    expect(buildAccountUpdate(base, form({ tags: 'us, prod' }))).toEqual({ tags: ['us', 'prod'] })
  })

  it('全无改动 → noop(不发空 PATCH)', () => {
    expect(buildAccountUpdate(base, form({}))).toEqual({ noop: true })
  })

  it('数字非法 → 报错', () => {
    expect(buildAccountUpdate(base, form({ priority: '-1' }))).toEqual({ error: '优先级必须是非负整数' })
    expect(buildAccountUpdate(base, form({ capConcurrency: 'x' }))).toEqual({ error: '并发上限必须是非负整数' })
  })

  it('reason 仅在有改动时附带,无改动不带 reason', () => {
    // noop 优先于 reason:即便填了 reason,无字段改动仍是 noop。
    expect(buildAccountUpdate(base, form({ reason: '随便写写' }))).toEqual({ noop: true })
  })
})
