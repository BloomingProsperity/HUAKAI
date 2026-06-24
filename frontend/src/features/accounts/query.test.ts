import { describe, expect, it } from 'vitest'
import { buildAccountListQuery, EMPTY_ACCOUNT_FILTERS } from './query'

/*
 * 守 buildAccountListQuery 的核心契约:空筛选项必须【省略】(不传空串),否则后端
 * GET /admin/v1/provider-accounts 会对空 state_filter/pool_group_id 报 400;limit 必须
 * 夹紧到 [1,200];cursor 仅非空时带。
 */
describe('buildAccountListQuery', () => {
  it('空筛选只带 limit,其余键省略(防后端 400)', () => {
    const q = buildAccountListQuery(EMPTY_ACCOUNT_FILTERS)
    expect(q).toEqual({
      limit: 50,
      state_filter: undefined,
      pool_group_id: undefined,
      tag: undefined,
      cursor: undefined,
    })
    // 判别核心:空项不能是 '' —— 变异(把 `|| undefined` 改成原值)会让这些变成 '' 而 RED。
    expect(q.state_filter).toBeUndefined()
    expect(q.pool_group_id).toBeUndefined()
  })

  it('非空筛选项按值带上,字符串去空白', () => {
    const q = buildAccountListQuery({
      stateFilter: 'rate_limited',
      poolGroupId: '  12 ',
      tag: ' prod ',
      cursor: 'abc123',
      limit: 50,
    })
    expect(q.state_filter).toBe('rate_limited')
    expect(q.pool_group_id).toBe('12')
    expect(q.tag).toBe('prod')
    expect(q.cursor).toBe('abc123')
  })

  it('limit 夹紧到 [1,200],非法回退默认 50', () => {
    expect(buildAccountListQuery({ ...EMPTY_ACCOUNT_FILTERS, limit: 999 }).limit).toBe(200)
    expect(buildAccountListQuery({ ...EMPTY_ACCOUNT_FILTERS, limit: 0 }).limit).toBe(50)
    expect(buildAccountListQuery({ ...EMPTY_ACCOUNT_FILTERS, limit: -5 }).limit).toBe(1)
    expect(buildAccountListQuery({ ...EMPTY_ACCOUNT_FILTERS, limit: 30 }).limit).toBe(30)
  })
})
