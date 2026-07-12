import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))
vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import { getProviderAccountRecentRequests } from './api'
import { resolveCredentialProject } from './credentialsApi'

describe('账号 recent-requests API', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({ items: [], source: 'settled_usage_records' })
  })

  it('锁定路径、GET、limit 与 signal', async () => {
    const ctrl = new AbortController()
    await getProviderAccountRecentRequests(99, 50, ctrl.signal)
    expect(client.get).toHaveBeenCalledWith('/admin/v1/provider-accounts/99/recent-requests', {
      query: { limit: 50 },
      signal: ctrl.signal,
    })
    expect(client.send).not.toHaveBeenCalled()
  })

  it('默认 limit=20', async () => {
    await getProviderAccountRecentRequests(7)
    expect(client.get).toHaveBeenCalledWith('/admin/v1/provider-accounts/7/recent-requests', { query: { limit: 20 }, signal: undefined })
  })
})

describe('resolve-project API', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.send.mockResolvedValue({ project_ref: 'proj-x' })
  })

  it('锁定路径、POST、body 带 tenant_id(变异:改错方法/路径/漏 tenant → RED)', async () => {
    await resolveCredentialProject(3, 12, { tenant_id: 8 })
    expect(client.send).toHaveBeenCalledWith(
      'POST',
      '/admin/v1/provider-accounts/3/credentials/12/resolve-project',
      { tenant_id: 8 },
    )
    expect(client.get).not.toHaveBeenCalled()
  })
})
