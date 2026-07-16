import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))
vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import { getTenantDefaultProxy, setTenantDefaultProxy } from './api'

describe('租户默认出口 API 接线', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({ proxy_id: null })
    client.send.mockResolvedValue({ proxy_id: null })
  })

  it('GET 使用 admin 路径和统一 apiGet，且透传 signal', async () => {
    const ctrl = new AbortController()
    await getTenantDefaultProxy(7, ctrl.signal)
    expect(client.get).toHaveBeenCalledWith('/admin/v1/tenants/7/default-proxy', { signal: ctrl.signal })
  })

  it('PUT 使用统一 apiSend，并保留 null 清除请求体', async () => {
    await setTenantDefaultProxy(7, { proxy_id: null })
    expect(client.send).toHaveBeenCalledWith('PUT', '/admin/v1/tenants/7/default-proxy', { proxy_id: null })
  })
})
