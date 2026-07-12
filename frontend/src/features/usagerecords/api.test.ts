import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn(), fresh: vi.fn() }))

vi.mock('../../lib/api', () => ({
  apiGet: client.get,
  apiSend: client.send,
  ensureFreshSessionForPath: client.fresh,
  ApiError: class ApiError extends Error {},
}))

import { getCostReceipt, verifyCostReceipt } from './api'

describe('用量详情回执 API 接线', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.fresh.mockReset()
    client.get.mockResolvedValue({})
    client.send.mockResolvedValue({})
  })

  it('读取回执按段编码 host/tail 形态，保留路由分隔斜杠', async () => {
    const controller = new AbortController()
    await getCostReceipt('host.example/tail abc', controller.signal)
    expect(client.get).toHaveBeenCalledWith('/v1/receipts/host.example/tail%20abc', {
      signal: controller.signal,
    })
  })

  it('验存储回执固定 POST /verify 且 body 必须为空', async () => {
    const controller = new AbortController()
    await verifyCostReceipt('host.example/tail abc', controller.signal)
    expect(client.send).toHaveBeenCalledWith(
      'POST',
      '/v1/receipts/host.example/tail%20abc/verify',
      undefined,
      { signal: controller.signal },
    )
  })
})
