import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))

vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import { listApiKeys } from './api'

describe('我的密钥 API', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({ api_keys: [], count: 0 })
  })

  it('只透传 session 端点需要的 offset/limit，不添加 tenant_id', async () => {
    const controller = new AbortController()
    await listApiKeys(200, 100, controller.signal)
    expect(client.get).toHaveBeenCalledWith('/v1/api-keys', {
      query: { offset: 200, limit: 100 },
      signal: controller.signal,
    })
    expect(client.send).not.toHaveBeenCalled()
  })
})
