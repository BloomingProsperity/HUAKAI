import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))

vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import { repriceBilling } from './api'

describe('计费重算 API 接线', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.send.mockResolvedValue({ object: 'billing_reprice_report', dry_run: true, items: [], summary: {} })
  })

  it('单条实际重算锁定 POST、真实路径与互斥 body', async () => {
    const controller = new AbortController()
    const body = { usage_record_id: 81, dry_run: false } as const
    await repriceBilling(body, controller.signal)
    expect(client.send).toHaveBeenCalledWith('POST', '/admin/v1/billing/reprice', body, {
      signal: controller.signal,
    })
  })

  it('租户时间窗预演锁定 RFC3339 字段与 limit，不添加后端未接收字段', async () => {
    const body = {
      tenant_id: 7,
      from: '2026-07-10T00:00:00.000Z',
      to: '2026-07-11T00:00:00.000Z',
      limit: 42,
      dry_run: true,
    } as const
    await repriceBilling(body)
    expect(client.send).toHaveBeenCalledWith('POST', '/admin/v1/billing/reprice', body, { signal: undefined })
    expect(client.send.mock.calls[0][2]).toEqual(body)
  })
})
