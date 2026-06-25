import { describe, expect, it } from 'vitest'
import { fmtLatencyMs, sparklinePoints, successRateTone } from './ops'

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
