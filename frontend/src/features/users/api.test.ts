import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))

vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import { getUserUsage, listUsers } from './api'

describe('管理员用户用量 API', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({ items: [], next_cursor: '' })
  })

  it('锁定用户作用域路径、GET 客户端、limit 与 signal', async () => {
    const controller = new AbortController()
    await getUserUsage(7, 17, 200, controller.signal)
    expect(client.get).toHaveBeenCalledOnce()
    expect(client.get).toHaveBeenCalledWith('/admin/v1/users/17/usage', {
      query: { tenant_id: 7, limit: 200 },
      signal: controller.signal,
    })
    expect(client.send).not.toHaveBeenCalled()
  })

  it('用户列表透传搜索、offset 与后端上限内的 limit', async () => {
    const controller = new AbortController()
    await listUsers(7, '  ops@example.com  ', 100, 100, controller.signal)
    expect(client.get).toHaveBeenCalledWith('/admin/v1/users', {
      query: { tenant_id: 7, q: 'ops@example.com', offset: 100, limit: 100 },
      signal: controller.signal,
    })
  })
})
