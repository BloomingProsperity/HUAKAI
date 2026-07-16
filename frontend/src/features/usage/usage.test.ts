import { describe, expect, it } from 'vitest'
import type { ApiKeyView } from '../keys/types'
import type { KeyUsageSummary } from './types'
import { mapKeyUsageRows, mapUsageStats } from './usage'

function key(id: number, name: string): ApiKeyView {
  return { api_key_id: id, name, key_prefix: `hk-${id}`, status: 'active', created_at: '', updated_at: '' }
}

function summary(id: number, cost: string, requests: number): KeyUsageSummary {
  return {
    api_key_id: id,
    total_cost: cost,
    request_count: requests,
    total_tokens_input: 1200,
    total_tokens_output: 340,
    total_cache_read_tokens: 56,
    total_cache_creation_tokens: 7,
  }
}

describe('用量页纯映射', () => {
  it('六列数据完整映射，失败汇总不伪装成零', () => {
    // 判别核心:读/写缓存两列都必须进入合并列，不可用行必须显式保留。
    expect(mapKeyUsageRows([
      { key: key(1, '生产'), summary: summary(1, '1.2500', 1234) },
      { key: key(2, '备用'), summary: null },
    ])).toEqual([
      { id: 1, name: '生产', prefix: 'hk-1', cost: '1.2500', requests: '1,234', inputTokens: '1,200', outputTokens: '340', cacheTokens: '56/7', available: true },
      { id: 2, name: '备用', prefix: 'hk-2', cost: '—', requests: '—', inputTokens: '—', outputTokens: '—', cacheTokens: '—', available: false },
    ])
  })

  it('当前页统计正确求和且不丢失汇总不可用的 Key', () => {
    const stats = mapUsageStats([
      { key: key(1, 'A'), summary: summary(1, '1.25', 7) },
      { key: key(2, 'B'), summary: summary(2, '2.50', 11) },
      { key: key(3, 'C'), summary: null },
    ])
    // 判别核心:花费必须相加而非取首项，请求数不能误用 Key 数。
    expect(stats).toEqual([
      { label: '活跃 Key 数', value: '3', hint: '1 个 Key 汇总暂不可用' },
      { label: '合计花费', value: '$3.7500', hint: '当前页 USD' },
      { label: '合计请求', value: '18', hint: '当前页已取得汇总' },
    ])
  })

  it('空页加载时不短暂显示零值', () => {
    expect(mapUsageStats([], true).map((stat) => stat.value)).toEqual(['…', '…', '…'])
  })

  it('加载失败时不把未知用量伪装成零', () => {
    expect(mapUsageStats([], false, true).map((stat) => stat.value)).toEqual(['—', '—', '—'])
  })
})
