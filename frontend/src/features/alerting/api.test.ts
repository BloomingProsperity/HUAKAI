import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))
vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import { fetchMetricCatalog } from './api'

describe('告警指标目录 API 接线', () => {
  beforeEach(() => client.get.mockReset())

  it('从管理员规则目录端点读取且透传取消信号', async () => {
    const ctrl = new AbortController()
    client.get.mockResolvedValueOnce([])
    await fetchMetricCatalog(ctrl.signal)
    expect(client.get).toHaveBeenCalledWith('/v1/admin/alert-rules/metric-catalog', { signal: ctrl.signal })
  })
})
