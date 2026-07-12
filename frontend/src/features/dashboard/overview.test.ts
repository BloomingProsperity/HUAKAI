import { describe, expect, it } from 'vitest'
import type { AlertEvent } from '../alerting/types'
import type { HealthScoreResponse, OverviewResponse, PerfMetricsResponse } from '../ops/types'
import { alertRows, healthStatItem, overviewStatItems, perfStatItems, trendRequestValues } from './overview'

const overview: OverviewResponse = {
  window: '24h',
  totals: {
    requests: 18200,
    total_cost: '12.40',
    total_tokens: 960000,
    active_users: 86,
    active_api_keys: 41,
    success_count: 18000,
    error_count: 200,
    success_rate: '0.9890',
  },
  trend: [
    { day: '2026-07-10', requests: 5000, cost: '3.10' },
    { day: '2026-07-11', requests: 6200, cost: '4.20' },
    { day: '2026-07-12', requests: 7000, cost: '5.10' },
  ],
}

describe('overviewStatItems', () => {
  // 变异:卡片取错字段(如把 active_users 填进请求卡)或漏卡 → 本测红。
  it('六卡按序映射且值来自对应字段', () => {
    const items = overviewStatItems(overview)
    expect(items.map((i) => i.label)).toEqual(['请求数', '成功率', '成本', 'Tokens', '活跃用户', '活跃 Key'])
    expect(items[0].value).toBe('18,200')
    expect(items[0].hint).toContain('失败 200')
    expect(items[1].value).toBe('98.90%')
    expect(items[2].value).toBe('$12.40')
    expect(items[4].value).toBe('86')
    expect(items[5].value).toBe('41')
  })
})

describe('perfStatItems + healthStatItem', () => {
  it('p95/错误率与健康分渠道信号', () => {
    const perf: PerfMetricsResponse = {
      window: '24h',
      summary: { avg_ttft_ms: '812', avg_tps: '35.2', request_count: 100, error_count: 3, error_rate: '0.0300' },
      latency_percentiles_ms: { p50: 900, p95: 1800, p99: 3200 },
    }
    const items = perfStatItems(perf)
    expect(items[0].value).toBe('1.80s')
    expect(items[0].hint).toContain('p99')
    expect(items[1].value).toBe('3.00%')

    const health: HealthScoreResponse = {
      window: '24h',
      overall_score: 92,
      business_score: 95,
      infra_score: 88,
      signals: {
        error_rate: '0.01',
        ttft_p99_ms: 3000,
        channel_health_available: true,
        healthy_channels: 9,
        managed_channels: 10,
      },
    }
    const h = healthStatItem(health)
    expect(h.value).toBe('92')
    expect(h.hint).toBe('渠道 9/10 健康')
    // 渠道信号不可用 → hint 回退业务/设施分(变异:恒走渠道分支 → 红)。
    const noChan = healthStatItem({ ...health, signals: { ...health.signals, channel_health_available: false } })
    expect(noChan.hint).toBe('业务 95 · 设施 88')
  })
})

describe('trendRequestValues', () => {
  it('取请求序列保持顺序', () => {
    expect(trendRequestValues(overview.trend)).toEqual([5000, 6200, 7000])
  })
})

describe('alertRows', () => {
  const evt = (id: number, state: string): AlertEvent => ({
    id,
    tenant_id: 1,
    rule_id: 7,
    state,
    observed_value: 1.2,
    fired_at: '2026-07-12T15:04:00Z',
    email_sent: false,
  })
  it('截断到 limit,firing 标红且文案区分状态', () => {
    const rows = alertRows([evt(1, 'firing'), evt(2, 'resolved'), evt(3, 'firing')], 2)
    expect(rows).toHaveLength(2)
    expect(rows[0].firing).toBe(true)
    expect(rows[0].text).toContain('告警中')
    expect(rows[0].text).toContain('规则 #7')
    expect(rows[1].firing).toBe(false)
    expect(rows[1].text).toContain('已恢复')
  })
  it('坏时间戳原样透出不崩', () => {
    const rows = alertRows([{ ...evt(9, 'firing'), fired_at: 'not-a-date' }], 5)
    expect(rows[0].text).toContain('not-a-date')
  })
})
