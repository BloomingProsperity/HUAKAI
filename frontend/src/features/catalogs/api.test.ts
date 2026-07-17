import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))
vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import { createChannel, getChannel, listChannels, updateChannel } from './api'
import type { ChannelCatalogMutationRequest } from './types'

describe('channel 三门 API 接线', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({ items: [] })
    client.send.mockResolvedValue({})
  })

  it('list 精确携带租户作用域与分页参数', async () => {
    const ctrl = new AbortController()
    await listChannels(7, 100, 0, ctrl.signal)

    expect(client.get).toHaveBeenCalledTimes(1)
    expect(client.get).toHaveBeenCalledWith('/admin/v1/channels', {
      query: { tenant_id: 7, limit: 100, offset: 0 },
      signal: ctrl.signal,
    })
  })

  it('get 精确携带 id、租户作用域与取消信号', async () => {
    const ctrl = new AbortController()
    await getChannel(91, 7, ctrl.signal)

    expect(client.get).toHaveBeenCalledTimes(1)
    expect(client.get).toHaveBeenCalledWith('/admin/v1/channels/91', {
      query: { tenant_id: 7 },
      signal: ctrl.signal,
    })
  })

  it('create 与 update 精确下发三门，不把对象转成字符串', async () => {
    const body: ChannelCatalogMutationRequest = {
      pool_group_id: 73,
      name: 'rewrite-gates',
      enabled: true,
      body_param_strips: ['drop_create'],
      param_override: { temperature: 0.25, metadata: { source: 'frontend' } },
      sensitive_words: ['word_create'],
    }
    await createChannel(7, body)
    await updateChannel(7, 91, body)

    expect(client.send).toHaveBeenNthCalledWith(1, 'POST', '/admin/v1/channels', body, {
      query: { tenant_id: 7 },
    })
    expect(client.send).toHaveBeenNthCalledWith(2, 'PUT', '/admin/v1/channels/91', body, {
      query: { tenant_id: 7 },
    })
    const sent = client.send.mock.calls[1][2] as ChannelCatalogMutationRequest
    expect(sent).toEqual(body)
    expect(typeof sent.param_override).toBe('object')
  })
})
