import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))
vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import { createBinding, deleteBinding, listBindings, updateBinding } from './api'

describe('路由绑定 API 租户作用域', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({ items: [] })
    client.send.mockResolvedValue({})
  })

  it('列表透传 tenant_id、筛选条件与 signal', async () => {
    const ctrl = new AbortController()
    await listBindings(7, { modelId: ' 11 ', poolGroupId: ' 13 ' }, ctrl.signal)
    expect(client.get).toHaveBeenCalledWith('/admin/v1/model-pool-bindings', {
      query: { tenant_id: 7, model_id: '11', pool_group_id: '13' },
      signal: ctrl.signal,
    })
  })

  it('创建、更新和删除均在 query 透传 tenant_id', async () => {
    const createBody = { model_id: 11, pool_group_id: 13 }
    const updateBody = {
      priority: 100,
      selection_mode: 'strict_priority',
      enabled: false,
    }

    await createBinding(createBody, 7)
    await updateBinding(17, updateBody, 7)
    await deleteBinding(17, 7)

    expect(client.send).toHaveBeenNthCalledWith(1, 'POST', '/admin/v1/model-pool-bindings', createBody, { query: { tenant_id: 7 } })
    expect(client.send).toHaveBeenNthCalledWith(2, 'PATCH', '/admin/v1/model-pool-bindings/17', updateBody, { query: { tenant_id: 7 } })
    const sentUpdate = client.send.mock.calls[1][2] as Record<string, unknown>
    expect(sentUpdate).toMatchObject({ priority: 100, selection_mode: 'strict_priority', enabled: false })
    expect('weight' in sentUpdate).toBe(false)
    expect('max_parallel_requests' in sentUpdate).toBe(false)
    expect('fallback_class' in sentUpdate).toBe(false)
    expect(client.send).toHaveBeenNthCalledWith(3, 'DELETE', '/admin/v1/model-pool-bindings/17', undefined, { query: { tenant_id: 7 } })
  })
})
