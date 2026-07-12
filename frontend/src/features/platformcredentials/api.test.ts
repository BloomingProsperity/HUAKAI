import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({
  get: vi.fn(),
  send: vi.fn(),
}))

vi.mock('../../lib/api', () => ({
  apiGet: client.get,
  apiSend: client.send,
}))

import {
  createAdminToken,
  createPlatformApiKey,
  listAdminTokens,
  listPlatformApiKeys,
  revokeAdminToken,
  revokePlatformApiKey,
} from './api'

describe('平台凭证 API', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({ items: [], limit: 100, offset: 0 })
    client.send.mockResolvedValue({ id: 1, already_revoked: false })
  })

  it('运维令牌列表走统一 apiGet，并锁定分页参数', async () => {
    const controller = new AbortController()
    await listAdminTokens(80, 20, controller.signal)
    expect(client.get).toHaveBeenCalledOnce()
    expect(client.get).toHaveBeenCalledWith('/admin/v1/admin-tokens', {
      query: { limit: 80, offset: 20 },
      signal: controller.signal,
    })
  })

  it('运维令牌签发与吊销锁定 POST、路径和 body', async () => {
    const body = { role: 'tenant_operator' as const, tenant_id: 7, expires_at: '2026-07-13T00:00:00.000Z' }
    await createAdminToken(body)
    await revokeAdminToken(19, '轮换')
    expect(client.send.mock.calls).toEqual([
      ['POST', '/admin/v1/admin-tokens', body],
      ['POST', '/admin/v1/admin-tokens/19/revoke', { reason: '轮换' }],
    ])
  })

  it('平台 API Key 列表始终携带 tenant_id，且不改写 signal', async () => {
    const controller = new AbortController()
    await listPlatformApiKeys(9, 50, 10, controller.signal)
    expect(client.get).toHaveBeenCalledWith('/admin/v1/api-keys', {
      query: { tenant_id: 9, limit: 50, offset: 10 },
      signal: controller.signal,
    })
  })

  it('平台 API Key 签发与吊销锁定真实请求形状', async () => {
    const body = { tenant_id: 9, user_id: 4, name: '生产主 Key', environment: 'live' as const }
    await createPlatformApiKey(body)
    await revokePlatformApiKey(23, 9, '泄漏处置')
    expect(client.send.mock.calls).toEqual([
      ['POST', '/admin/v1/api-keys', body],
      ['POST', '/admin/v1/api-keys/23/revoke', { tenant_id: 9, reason: '泄漏处置' }],
    ])
  })
})
