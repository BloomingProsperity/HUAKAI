import { describe, expect, it } from 'vitest'
import { mapKeyPagination, mapKeyRows, mapKeyStats } from './keys'
import type { ApiKeyView } from './types'

describe('密钥页数据映射', () => {
  it('统计总数严格使用后端 count，状态数只取当前页', () => {
    const stats = mapKeyStats([
      key({ api_key_id: 1, status: 'active' }),
      key({ api_key_id: 2, status: 'revoked' }),
      key({ api_key_id: 3, status: 'expired' }),
    ], 259)

    // 判别核心：把当前页 length(3)误作总数会使首卡断言变红。
    expect(stats.map(({ label, value, hint }) => ({ label, value, hint }))).toEqual([
      { label: '总数', value: '259 个', hint: '全部密钥，不受当前分页影响' },
      { label: '活跃', value: '1 个', hint: '当前页口径' },
      { label: '已撤销', value: '1 个', hint: '当前页口径' },
    ])
    expect(mapKeyStats([], null).every((card) => card.value === '—')).toBe(true)
  })

  it('表格行保留脱敏前缀并区分空时间与非法时间', () => {
    const source = key({
      key_prefix: 'hk_live_abcd',
      status: 'revoked',
      expires_at: null,
      last_used_at: 'not-a-date',
      created_at: '2026-07-13T00:00:00Z',
    })
    const row = mapKeyRows([source])[0]

    // 判别核心：状态若恒映射为“活跃”，该断言会变红。
    expect(row.statusText).toBe('已撤销')
    expect(row.statusTone).toBe('muted')
    expect(row.prefix).toBe('hk_live_abcd')
    expect(row.expiresAt).toBe('永不')
    expect(row.lastUsedAt).toBe('从未')
    expect(row.createdAt).toBe(new Date(source.created_at).toLocaleString('zh-CN', { hour12: false }))
  })

  it('分页依据 259 全量 count 开放后续页并在末页关闭下一页', () => {
    const first = mapKeyPagination({ offset: 0, limit: 100, returnedCount: 100, totalCount: 259 })
    // 判别核心：若用 returnedCount 当总数，第一页 canNext 会错误变为 false。
    expect(first).toEqual({
      page: 1,
      start: 1,
      end: 100,
      canPrevious: false,
      canNext: true,
      scopeText: '第 1–100 条 · 共 259 个',
    })
    const last = mapKeyPagination({ offset: 200, limit: 100, returnedCount: 59, totalCount: 259 })
    expect(last.canPrevious).toBe(true)
    expect(last.canNext).toBe(false)
    expect(last.scopeText).toBe('第 201–259 条 · 共 259 个')
  })
})

function key(overrides: Partial<ApiKeyView>): ApiKeyView {
  return {
    api_key_id: 1,
    name: '生产 Key',
    key_prefix: 'hk_live_test',
    status: 'active',
    created_at: '2026-07-13T00:00:00Z',
    updated_at: '2026-07-13T00:00:00Z',
    ...overrides,
  }
}
