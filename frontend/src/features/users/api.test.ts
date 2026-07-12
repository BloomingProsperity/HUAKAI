import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))

vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import { getUserUsage } from './api'

describe('管理员用户用量 API', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({ items: [], next_cursor: '' })
  })

  it('锁定用户作用域路径、GET 客户端、limit 与 signal', async () => {
    const controller = new AbortController()
    await getUserUsage(17, 200, controller.signal)
    expect(client.get).toHaveBeenCalledOnce()
    expect(client.get).toHaveBeenCalledWith('/admin/v1/users/17/usage', {
      query: { limit: 200 },
      signal: controller.signal,
    })
    expect(client.send).not.toHaveBeenCalled()
  })
})
