import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))

vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import { listObsDlq, replayObsDlq, replayUsageRecordDlq } from './api'

describe('死信 API 接线', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({ items: [] })
    client.send.mockResolvedValue({ replayed: true })
  })

  it('观测死信列表锁定真实路径、tenant 参数名和全部筛选字段', async () => {
    const controller = new AbortController()
    await listObsDlq({
      tenantId: 7,
      eventType: 'email.retry',
      from: '2026-07-01T00:00:00Z',
      to: '2026-07-02T00:00:00Z',
      limit: 80,
      signal: controller.signal,
    })
    expect(client.get).toHaveBeenCalledOnce()
    expect(client.get).toHaveBeenCalledWith('/admin/v1/obs-dlq', {
      query: {
        tenant: 7,
        event_type: 'email.retry',
        from: '2026-07-01T00:00:00Z',
        to: '2026-07-02T00:00:00Z',
        limit: 80,
      },
      signal: controller.signal,
    })
  })

  it('观测死信重放编码不透明 ID，并固定 POST', async () => {
    await replayObsDlq('dead/id 9')
    expect(client.send).toHaveBeenCalledWith('POST', '/admin/v1/obs-dlq/dead%2Fid%209/replay')
  })

  it('用量记录分签使用专用重放路由', async () => {
    await replayUsageRecordDlq(42)
    expect(client.send).toHaveBeenCalledWith('POST', '/admin/v1/usage-record-dlq/42/replay')
  })
})
