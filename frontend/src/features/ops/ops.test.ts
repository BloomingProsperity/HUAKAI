import { describe, expect, it } from 'vitest'
import {
  fmtFractionPct,
  fmtLatencyMs,
  healthScoreTone,
  mapHealthScoreRows,
  mapLeaderboardRows,
  mapOverviewStats,
  mapPerfBucketRows,
  mapPerfMetricStats,
  mapPerformanceRows,
  mapProviderAccountRows,
  sparklinePoints,
  successRateTone,
  totalTokens,
  windowToRange,
} from './ops'

describe('sparklinePoints', () => {
  it('Y 轴翻转 + 归一化:低值落底、高值落顶', () => {
    // 判别核心:值 0 应在底部(y=height),值 10 应在顶部(y=0)。
    // 变异(不翻转 Y,y = (v-min)/span*height)→ 会得 "0,0 100,20",本断言 RED。
    expect(sparklinePoints([0, 10], 100, 20, 0)).toBe('0,20 100,0')
  })

  it('多点等距铺开 X', () => {
    // 三点 → x 为 0 / 50 / 100;中间值 5 → y 居中 10。
    expect(sparklinePoints([0, 5, 10], 100, 20, 0)).toBe('0,20 50,10 100,0')
  })

  it('空序列空串;单点居中', () => {
    expect(sparklinePoints([], 100, 20)).toBe('')
    expect(sparklinePoints([7], 100, 20, 0)).toBe('50,10')
  })

  it('全等值不除零 → 居中平线(不压到底/顶)', () => {
    // 三个相等值 → 不应 NaN/压边,画居中平线(y=height/2=10)。
    expect(sparklinePoints([5, 5, 5], 100, 20, 0)).toBe('0,10 50,10 100,10')
  })
})

describe('successRateTone', () => {
  it('入参是 0~1 小数(后端 success_rate 形态):≥0.99 绿、≥0.95 警、否则危、非法警', () => {
    // 判别核心:入参是 0~1 小数(如 "0.9950"=99.5%),不是百分数。
    // 变异(误按 0~100 比,v>=99)→ "0.9950" 落 danger,首断言 RED(即便 99.5% 成功也显示告警)。
    expect(successRateTone('0.9950')).toBe('ok')
    expect(successRateTone('0.97')).toBe('warn')
    expect(successRateTone('0.90')).toBe('danger')
    expect(successRateTone('1')).toBe('ok') // 100% 成功
    expect(successRateTone('abc')).toBe('warn')
  })
})

describe('fmtLatencyMs', () => {
  it('≥1000 显示秒、否则毫秒整数', () => {
    expect(fmtLatencyMs(1500)).toBe('1.50s')
    expect(fmtLatencyMs(320.7)).toBe('321ms')
    expect(fmtLatencyMs(NaN)).toBe('—')
  })
})

describe('windowToRange', () => {
  const now = new Date('2026-06-29T12:00:00.000Z')

  it('to=now、from=now-跨度;24h/7d/30d 各取对应天数', () => {
    // 判别核心:from 必须正好回退对应天数。变异(span 用错档/不减)→ from 断言 RED。
    expect(windowToRange('24h', now)).toEqual({
      from: '2026-06-28T12:00:00.000Z',
      to: '2026-06-29T12:00:00.000Z',
    })
    expect(windowToRange('7d', now).from).toBe('2026-06-22T12:00:00.000Z')
    expect(windowToRange('30d', now).from).toBe('2026-05-30T12:00:00.000Z')
  })

  it('未知 window 回退 7 天(不返回 to≤from 的非法区间)', () => {
    // 判别核心:未知档必须回退 7d。变异(回退 0 或 undefined→NaN)→ from 不等于 7 天前,RED。
    const r = windowToRange('999x', now)
    expect(r.from).toBe('2026-06-22T12:00:00.000Z')
    expect(new Date(r.to).getTime()).toBeGreaterThan(new Date(r.from).getTime())
  })
})

describe('fmtFractionPct', () => {
  it('0~1 小数乘 100 显示为百分比(2 位)', () => {
    // 判别核心:必须乘 100。变异(不乘 100)→ 得 "0.01%" 而非 "1.23%",RED。
    expect(fmtFractionPct('0.0123')).toBe('1.23%')
    expect(fmtFractionPct('0')).toBe('0.00%')
    expect(fmtFractionPct('1')).toBe('100.00%')
    expect(fmtFractionPct('abc')).toBe('—')
  })
})

describe('healthScoreTone', () => {
  it('≥90 绿、≥70 警、否则危、非法警', () => {
    // 判别核心:边界落档。变异(把 90 改 80 或去掉 90 档)→ 89/90 断言 RED。
    expect(healthScoreTone(100)).toBe('ok')
    expect(healthScoreTone(90)).toBe('ok')
    expect(healthScoreTone(89)).toBe('warn')
    expect(healthScoreTone(70)).toBe('warn')
    expect(healthScoreTone(69)).toBe('danger')
    expect(healthScoreTone(NaN)).toBe('warn')
  })
})

describe('totalTokens', () => {
  it('总 Token = 输入 + 输出', () => {
    // 判别核心:两列都要加。变异(漏加 output)→ 30 ≠ 10,RED。
    expect(totalTokens({ total_input_tokens: 10, total_output_tokens: 20 })).toBe(30)
    expect(totalTokens({ total_input_tokens: 0, total_output_tokens: 0 })).toBe(0)
  })
})

describe('运维大屏底座映射', () => {
  it('总览六卡保留顺序、格式并把成功率映射为三档 tone', () => {
    const stats = mapOverviewStats({
      window: '7d',
      totals: {
        requests: 9552,
        total_cost: '241.2743402048',
        total_tokens: 1234567,
        active_users: 18,
        active_api_keys: 23,
        success_count: 9500,
        error_count: 52,
        success_rate: '0.9950',
      },
      trend: [],
    })

    // 判别核心:成功率必须是第六卡且沿用 0~1 阈值；变异顺序、格式或 tone 均 RED。
    expect(stats).toEqual([
      { label: '请求数', value: '9,552', tone: 'default' },
      { label: '总成本', value: '$241.27', valueTitle: '$241.2743402048', tone: 'default' },
      { label: '总 Token', value: '1,234,567', tone: 'default' },
      { label: '活跃用户', value: '18', tone: 'default' },
      { label: '活跃 Key', value: '23', tone: 'default' },
      { label: '成功率', value: '99.50%', hint: '健康', tone: 'ok' },
    ])
    expect(mapOverviewStats(null)).toHaveLength(6)
    expect(mapOverviewStats(null).every((stat) => stat.value === '…')).toBe(true)
  })

  it('性能分位六卡保留毫秒、秒与小数百分比口径', () => {
    const stats = mapPerfMetricStats({
      window: '7d',
      summary: {
        avg_ttft_ms: '3950.1503',
        avg_tps: '164.2553',
        request_count: 100,
        error_count: 2,
        error_rate: '0.0200',
      },
      latency_percentiles_ms: { p50: 400, p95: 1500, p99: 2600 },
    })

    // 判别核心:error_rate 必须乘 100，延迟达到 1000ms 必须换算秒；任一映射串列即 RED。
    expect(stats.map((stat) => [stat.label, stat.value])).toEqual([
      ['P50 延迟', '400ms'],
      ['P95 延迟', '1.50s'],
      ['P99 延迟', '2.60s'],
      ['平均 TTFT', '3.95s'],
      ['平均 TPS', '164.3'],
      ['错误率', '2.00%'],
    ])
    // 判别核心：卡片短值与悬浮全精度必须来自同一个原始指标。
    expect(stats[3]).toMatchObject({ valueTitle: '3950.1503ms' })
    expect(stats[4]).toMatchObject({ valueTitle: '164.2553' })
  })

  it('五张只读表的行映射不丢字段、不混淆输入输出 Token', () => {
    expect(mapLeaderboardRows([{ rank: 1, key: 'model-a', total_cost: '12.34', total_tokens: 3000, request_count: 20 }])).toEqual([
      { rank: 1, model: 'model-a', cost: '$12.34', costTitle: '$12.34', tokens: '3,000', requests: '20' },
    ])

    expect(mapHealthScoreRows({
      window: '7d',
      overall_score: 91,
      business_score: 72,
      infra_score: 68,
      signals: {
        error_rate: '0.0123',
        ttft_p99_ms: 1500,
        channel_health_available: true,
        healthy_channels: 4,
        managed_channels: 5,
      },
    })).toEqual([
      { id: 'overall', metric: '综合分', value: '91', statusText: '健康', statusTone: 'ok' },
      { id: 'business', metric: '业务面', value: '72', statusText: '注意', statusTone: 'warn' },
      { id: 'infra', metric: '基础设施面', value: '68', statusText: '告警', statusTone: 'danger' },
      { id: 'error-rate', metric: '错误率', value: '1.23%', statusText: '观测', statusTone: 'muted' },
      { id: 'ttft-p99', metric: 'TTFT P99', value: '1.50s', statusText: '观测', statusTone: 'muted' },
    ])

    expect(mapPerformanceRows([{ rank: 2, key: '', avg_ttft_ms: '90', avg_tps: '8.5', request_count: 7, error_rate: '0.1000' }])).toEqual([
      { rank: 2, key: '—', avgTtft: '90ms', avgTtftTitle: '90ms', avgTps: '8.5', avgTpsTitle: '8.5', requests: '7', errorRate: '10.00%' },
    ])

    expect(mapPerfBucketRows([{ bucket: '2026-07-13T10:00:00Z', key: 'model-b', avg_ttft_ms: '110', avg_tps: '9.2', request_count: 12, error_count: 3, error_rate: '0.2500' }])).toEqual([
      { id: '2026-07-13T10:00:00Z-model-b-0', bucket: '2026-07-13T10:00:00Z', model: 'model-b', avgTtft: '110ms', avgTtftTitle: '110ms', avgTps: '9.2', avgTpsTitle: '9.2', requests: '12', errors: '3', errorRate: '25.00%' },
    ])

    // 判别核心:合计必须为输入 1,000 + 输出 250；变异成单列或调换字段即 RED。
    expect(mapProviderAccountRows([{ provider_account_id: 9, request_count: 8, total_input_tokens: 1000, total_output_tokens: 250, total_cost: '4.56' }])).toEqual([
      { id: 9, account: '#9', requests: '8', inputTokens: '1,000', outputTokens: '250', tokens: '1,250', cost: '$4.56', costTitle: '$4.56' },
    ])
  })
})
