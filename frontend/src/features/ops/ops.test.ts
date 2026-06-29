import { describe, expect, it } from 'vitest'
import {
  fmtFractionPct,
  fmtLatencyMs,
  healthScoreTone,
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
  it('≥99 绿、≥95 警、否则危、非法警', () => {
    expect(successRateTone('99.5')).toBe('ok')
    expect(successRateTone('97')).toBe('warn')
    expect(successRateTone('90')).toBe('danger')
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
