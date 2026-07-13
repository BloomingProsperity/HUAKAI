import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))
vi.mock('../../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../../lib/api')>('../../lib/api')
  return { ...actual, apiGet: client.get, apiSend: client.send }
})

import { getProviderAccountHealthSummary } from './api'
import { healthStateLabel, isAttentionState } from './HealthSummaryCard'

describe('healthStateLabel / isAttentionState', () => {
  it('健康态中文标签', () => {
    expect(healthStateLabel('healthy')).toBe('健康')
    expect(healthStateLabel('throttled')).toBe('限流中')
    expect(healthStateLabel('revoked')).toBe('已吊销')
    expect(healthStateLabel('unknown')).toBe('unknown')
  })
  it('非 healthy 即需关注(变异:若把 throttled 当健康则漏报)', () => {
    expect(isAttentionState('healthy')).toBe(false)
    expect(isAttentionState('throttled')).toBe(true)
    expect(isAttentionState('revoked')).toBe(true)
  })
})

describe('health-summary API', () => {
  it('锁定路径与 GET(变异:改错路径 → RED)', async () => {
    client.get.mockReset()
    client.get.mockResolvedValue({ total: 0, enabled: 0, disabled: 0, needs_attention: 0, states: [] })
    await getProviderAccountHealthSummary(8)
    expect(client.get).toHaveBeenCalledWith('/admin/v1/provider-accounts/health-summary', { query: { tenant_id: 8 }, signal: undefined })
  })
})

// renderToStaticMarkup 保底:标签渲染冒烟(不触网络,组件懒加载后为 null 前的稳定态由纯函数覆盖)。
describe('渲染冒烟', () => {
  it('纯函数可用于组件渲染路径', () => {
    const html = renderToStaticMarkup(<span className={isAttentionState('throttled') ? 'hk-pill--crit' : 'hk-pill--ok'}>{healthStateLabel('throttled')}</span>)
    expect(html).toContain('限流中')
    expect(html).toContain('hk-pill--crit')
  })
})
