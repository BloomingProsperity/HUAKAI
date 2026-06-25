import { describe, expect, it } from 'vitest'
import {
  barRatio,
  clampRankingsLimit,
  DEFAULT_RANKINGS_LIMIT,
  formatCount,
  formatShare,
  MAX_RANKINGS_LIMIT,
  metricLabel,
  metricValue,
  parseShare,
  rankBy,
  type RankMetric,
} from './rankings'
import type { RankingEntry } from './types'

function entry(over: Partial<RankingEntry>): RankingEntry {
  return { rank: 0, model: 'm', request_count: 0, token_total: 0, request_share: '0', ...over }
}

describe('clampRankingsLimit', () => {
  it('正常整数原样', () => {
    expect(clampRankingsLimit(50)).toBe(50)
  })
  it('超过上限截到 MAX', () => {
    // 判别核心:>100 必须截到 100(变异去掉 cap → 1000 → RED)。
    expect(clampRankingsLimit(1000)).toBe(MAX_RANKINGS_LIMIT)
    expect(clampRankingsLimit(MAX_RANKINGS_LIMIT)).toBe(100)
  })
  it('0/负/非有限回落默认而非透传', () => {
    // 判别核心:不能透传 0(后端会 400),必须回落 DEFAULT。
    expect(clampRankingsLimit(0)).toBe(DEFAULT_RANKINGS_LIMIT)
    expect(clampRankingsLimit(-5)).toBe(20)
    expect(clampRankingsLimit(Number.NaN)).toBe(20)
  })
  it('小数向下取整', () => {
    expect(clampRankingsLimit(20.9)).toBe(20)
  })
})

describe('parseShare', () => {
  it('合法定点小数→数', () => {
    expect(parseShare('0.25')).toBe(0.25)
  })
  it('空/非法/负→0', () => {
    expect(parseShare(undefined)).toBe(0)
    expect(parseShare('abc')).toBe(0)
    expect(parseShare('-0.1')).toBe(0)
  })
})

describe('metricValue', () => {
  it('按指标取对应字段', () => {
    const e = entry({ request_count: 7, token_total: 999, request_share: '0.5' })
    expect(metricValue(e, 'request_count')).toBe(7)
    expect(metricValue(e, 'token_total')).toBe(999)
    expect(metricValue(e, 'request_share')).toBe(0.5)
  })
  it('负计数兜底为 0', () => {
    expect(metricValue(entry({ token_total: -3 }), 'token_total')).toBe(0)
  })
})

describe('rankBy', () => {
  const data: RankingEntry[] = [
    entry({ model: 'a', request_count: 10, token_total: 100 }),
    entry({ model: 'b', request_count: 30, token_total: 50 }),
    entry({ model: 'c', request_count: 30, token_total: 200 }),
  ]

  it('按调用次数降序并回填名次', () => {
    // 判别核心:必须降序(变异成升序 → 顺序反 → RED)。同值(b,c=30)按模型名升序 → b 在前。
    const out = rankBy(data, 'request_count')
    expect(out.map((e) => e.model)).toEqual(['b', 'c', 'a'])
    expect(out.map((e) => e.rank)).toEqual([1, 2, 3])
  })

  it('切到 token 维度名次随之变(c 跃居第一)', () => {
    // 判别核心:必须按所选 metric 排(变异成恒按 request_count → c 不会第一 → RED)。
    const out = rankBy(data, 'token_total')
    expect(out.map((e) => e.model)).toEqual(['c', 'a', 'b'])
    expect(out[0].rank).toBe(1)
  })

  it('不改原数组', () => {
    const snapshot = data.map((e) => e.model)
    rankBy(data, 'token_total')
    expect(data.map((e) => e.model)).toEqual(snapshot)
  })
})

describe('formatCount', () => {
  it('千分位', () => {
    expect(formatCount(1234567)).toBe('1,234,567')
  })
  it('负→0,非有限→—', () => {
    expect(formatCount(-1)).toBe('0')
    expect(formatCount(Number.POSITIVE_INFINITY)).toBe('—')
  })
})

describe('formatShare', () => {
  it('占比字符串→百分比(×100)', () => {
    // 判别核心:必须 ×100(变异去掉 → "0.12%" 而非 "12.35%" → RED)。
    expect(formatShare('0.123456')).toBe('12.35%')
    expect(formatShare('1')).toBe('100.00%')
  })
  it('空/非法→0.00%', () => {
    expect(formatShare(undefined)).toBe('0.00%')
    expect(formatShare('xyz')).toBe('0.00%')
  })
})

describe('barRatio', () => {
  const data: RankingEntry[] = [
    entry({ model: 'a', request_count: 100 }),
    entry({ model: 'b', request_count: 25 }),
  ]
  it('相对最大值的比例', () => {
    // 判别核心:必须除以全榜最大值(100),b=25 → 0.25。
    expect(barRatio(data[0], data, 'request_count')).toBe(1)
    expect(barRatio(data[1], data, 'request_count')).toBe(0.25)
  })
  it('全 0 → 0 不除零', () => {
    const zeros = [entry({ request_count: 0 }), entry({ request_count: 0 })]
    expect(barRatio(zeros[0], zeros, 'request_count')).toBe(0)
  })
})

describe('metricLabel', () => {
  it('给出中文标签', () => {
    const cases: Array<[RankMetric, string]> = [
      ['request_count', '调用次数'],
      ['token_total', '总 Token'],
      ['request_share', '调用占比'],
    ]
    for (const [m, label] of cases) expect(metricLabel(m)).toBe(label)
  })
})
