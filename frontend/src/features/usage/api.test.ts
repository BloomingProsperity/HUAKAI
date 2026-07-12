import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('../../lib/api', () => ({ apiGet: client.get }))

import { getKeyGeneration, getKeyUsageTimeSeries, listKeyUsageRecords } from './api'

describe('Key 级用量 API 接线', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.get.mockResolvedValue({ items: [], next_cursor: '', period: { from: '', to: '' } })
  })

  it('逐笔查询锁定真实路径、全部 query，并显式使用去空白 API Key Bearer', async () => {
    const controller = new AbortController()
    await listKeyUsageRecords(
      '  hk_live_secret  ',
      {
        limit: 80,
        cursor: 'opaque-cursor',
        from: '2026-07-01T00:00:00.000Z',
        to: '2026-07-13T00:00:00.000Z',
        model: 'gpt-5',
        provider: 'openai',
        status: 'success',
      },
      controller.signal,
    )
    expect(client.get).toHaveBeenCalledWith('/v1/me/usage', {
      bearer: 'hk_live_secret',
      query: {
        limit: 80,
        cursor: 'opaque-cursor',
        from: '2026-07-01T00:00:00.000Z',
        to: '2026-07-13T00:00:00.000Z',
        model: 'gpt-5',
        provider: 'openai',
        status: 'success',
      },
      signal: controller.signal,
    })
  })

  it('时间序列锁定 from/to/granularity 与 API Key Bearer', async () => {
    const query = {
      from: '2026-07-01T00:00:00.000Z',
      to: '2026-07-13T00:00:00.000Z',
      granularity: 'week' as const,
    }
    await getKeyUsageTimeSeries('hk_week', query)
    expect(client.get).toHaveBeenCalledWith('/v1/me/analytics/time-series', {
      bearer: 'hk_week',
      query,
      signal: undefined,
    })
  })

  it('单笔查询使用 id 参数而非 request_id，且不误带 session', async () => {
    await getKeyGeneration(' hk_detail ', ' req-123 ')
    expect(client.get).toHaveBeenCalledWith('/v1/generation', {
      bearer: 'hk_detail',
      query: { id: 'req-123' },
      signal: undefined,
    })
  })
})
