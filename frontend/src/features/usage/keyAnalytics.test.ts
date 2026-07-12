import { describe, expect, it } from 'vitest'
import {
  aggregateTokenCount,
  buildKeyAnalyticsWindow,
  costBarPercent,
  defaultKeyAnalyticsRange,
} from './keyAnalytics'
import type { KeyUsageTimeSeriesPoint } from './types'
import { formatLatency } from './KeyUsageAnalytics'

function point(cost: string): KeyUsageTimeSeriesPoint {
  return {
    day: '2026-07-12',
    requested_model: 'gpt-5',
    total_cost: cost,
    tokens: { input: 10, output: 20, cache_read: 3, cache_creation: 4 },
    request_count: 2,
  }
}

describe('Key 级分析时间窗口', () => {
  it('默认范围覆盖最近 30 个 UTC 日', () => {
    expect(defaultKeyAnalyticsRange(new Date('2026-07-12T18:20:00Z'))).toEqual({
      fromDay: '2026-06-13',
      toDay: '2026-07-12',
    })
  })

  it('同一天转为次日零点右界，完整覆盖当天', () => {
    expect(buildKeyAnalyticsWindow('2026-07-12', '2026-07-12')).toEqual({
      ok: true,
      value: {
        from: '2026-07-12T00:00:00.000Z',
        to: '2026-07-13T00:00:00.000Z',
      },
    })
  })

  it('31 天边界放行，32 天与反向范围拦截', () => {
    expect(buildKeyAnalyticsWindow('2026-06-12', '2026-07-12').ok).toBe(true)
    expect(buildKeyAnalyticsWindow('2026-06-11', '2026-07-12')).toEqual({
      ok: false,
      error: 'Key 级时间范围不能超过 31 天',
    })
    expect(buildKeyAnalyticsWindow('2026-07-13', '2026-07-12')).toEqual({
      ok: false,
      error: '开始日期不能晚于结束日期',
    })
  })

  it('不存在的日历日期不能被 Date 自动滚动后放行', () => {
    expect(buildKeyAnalyticsWindow('2026-02-30', '2026-03-01').ok).toBe(false)
  })
})

describe('Key 级时间序列展示逻辑', () => {
  it('费用条以最大值归一化，零费用不伪造宽度', () => {
    const low = point('1.00000000')
    const high = point('2.00000000')
    expect(costBarPercent(low, [low, high])).toBe(50)
    expect(costBarPercent(high, [low, high])).toBe(100)
    expect(costBarPercent(point('0'), [low, high])).toBe(0)
  })

  it('Token 汇总包含输入、输出与两类缓存', () => {
    expect(aggregateTokenCount(point('1'))).toBe(37)
  })
})

describe('formatLatency', () => {
  it('毫秒取整,缺失/负值省略(变异:null→0 会误显 0ms)', () => {
    expect(formatLatency(120.6)).toBe('121 ms')
    expect(formatLatency(0)).toBe('0 ms')
    expect(formatLatency(undefined)).toBe('—')
    expect(formatLatency(null)).toBe('—')
  })
})
