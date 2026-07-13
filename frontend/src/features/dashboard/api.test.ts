import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../lib/api', () => ({ apiGet: client.get }))

import {
  fetchAccountSummary,
  fetchAllFiringAlerts,
  fetchAllQuotaPolicies,
  fetchFiringAlerts,
  fetchModelLeaderboard,
  fetchPoolInventoryCount,
  fetchPoolSummary,
  fetchPricingModelCount,
  fetchQuotaPolicies,
  fetchRecentAuditEvents,
  fetchUsageOverview,
} from './api'

describe('运营总览 API 接线', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.get.mockResolvedValue({})
  })

  it('窗口数据显式选择 overview 与 model leaderboard', async () => {
    const ctrl = new AbortController()
    await fetchUsageOverview('7d', ctrl.signal)
    await fetchModelLeaderboard('7d', ctrl.signal)
    expect(client.get.mock.calls).toEqual([
      ['/v1/admin/usage/overview', { query: { window: '7d' }, signal: ctrl.signal }],
      ['/v1/admin/usage/leaderboard', { query: { by: 'model', window: '7d', limit: 100 }, signal: ctrl.signal }],
    ])
  })

  it('五个租户级 admin 端点全部带 tenant_id=1', async () => {
    const ctrl = new AbortController()
    await fetchAccountSummary(1, ctrl.signal)
    await fetchPoolSummary(1, ctrl.signal)
    await fetchQuotaPolicies(1, ctrl.signal)
    await fetchFiringAlerts(1, ctrl.signal)
    await fetchRecentAuditEvents(1, ctrl.signal)
    for (const call of client.get.mock.calls) {
      expect(call[1].query.tenant_id).toBe(1)
      expect(call[1].signal).toBe(ctrl.signal)
    }
    expect(client.get.mock.calls.map((call) => call[0])).toEqual([
      '/admin/v1/provider-accounts/health-summary',
      '/v1/admin/channel-health/summary',
      '/admin/v1/quota-policies',
      '/v1/admin/alert-events',
      '/admin/v1/audit-events',
    ])
  })

  it('账号池库存复用管理列表并按 items 长度计数', async () => {
    const ctrl = new AbortController()
    client.get.mockResolvedValueOnce({ items: Array.from({ length: 23 }, (_, id) => ({ id })) })
    await expect(fetchPoolInventoryCount(1, ctrl.signal)).resolves.toBe(23)
    expect(client.get).toHaveBeenCalledWith('/admin/v1/pools', { query: { tenant_id: 1, limit: 200 }, signal: ctrl.signal })
  })

  it('模型数使用公开价目数组长度，不再请求 API Key 模型目录', async () => {
    const ctrl = new AbortController()
    client.get.mockResolvedValueOnce([{ model: 'a' }, { model: 'b' }, { model: 'c' }])
    await expect(fetchPricingModelCount(ctrl.signal)).resolves.toBe(3)
    expect(client.get).toHaveBeenCalledWith('/v1/pricing/page', { signal: ctrl.signal })
    expect(client.get).not.toHaveBeenCalledWith('/v1/models', expect.anything())
  })

  it('配额与告警第一页满 200 条时继续分页并合并', async () => {
    const fullPolicies = Array.from({ length: 200 }, (_, id) => ({ id }))
    client.get.mockResolvedValueOnce({ items: fullPolicies }).mockResolvedValueOnce({ items: [{ id: 201 }] })
    const policies = await fetchAllQuotaPolicies(1)
    expect(policies).toHaveLength(201)
    expect(client.get.mock.calls.map((call) => call[1].query.offset)).toEqual([0, 200])

    client.get.mockReset()
    const fullAlerts = Array.from({ length: 200 }, (_, id) => ({ id }))
    client.get.mockResolvedValueOnce({ items: fullAlerts }).mockResolvedValueOnce({ items: [] })
    const alerts = await fetchAllFiringAlerts(1)
    expect(alerts).toHaveLength(200)
    expect(client.get.mock.calls.map((call) => call[1].query.offset)).toEqual([0, 200])
  })
})
