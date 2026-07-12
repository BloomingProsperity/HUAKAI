import { describe, expect, it } from 'vitest'
import { cacheHitRate, meterCells, resetCountdown } from './windowMeter'

describe('meterCells', () => {
  it('按百分比四舍五入填格', () => {
    expect(meterCells(0, 24)).toBe(0)
    expect(meterCells(100, 24)).toBe(24)
    expect(meterCells(50, 24)).toBe(12)
    // 54% × 24 = 12.96 → 13(判别:若用 floor 会得 12)
    expect(meterCells(54, 24)).toBe(13)
  })
  it('钳制越界与非法输入', () => {
    expect(meterCells(150, 24)).toBe(24)
    expect(meterCells(-10, 24)).toBe(0)
    expect(meterCells(NaN, 24)).toBe(0)
    expect(meterCells(50, 0)).toBe(0)
  })
})

describe('resetCountdown', () => {
  const now = Date.parse('2026-07-12T00:00:00Z')
  it('天级显示天+小时', () => {
    expect(resetCountdown('2026-07-15T02:00:00Z', now)).toBe('重置 3天2h 后')
    expect(resetCountdown('2026-07-15T00:00:00Z', now)).toBe('重置 3天 后')
  })
  it('小时级显示时+分', () => {
    expect(resetCountdown('2026-07-12T08:12:00Z', now)).toBe('重置 8h12m 后')
    expect(resetCountdown('2026-07-12T05:00:00Z', now)).toBe('重置 5h 后')
  })
  it('已过期与无效', () => {
    expect(resetCountdown('2026-07-11T00:00:00Z', now)).toBe('已结束')
    expect(resetCountdown('not-a-date', now)).toBe('')
  })
})

describe('cacheHitRate', () => {
  it('命中率=命中/(命中+输入)', () => {
    // 判别:2000 命中 + 8000 输入 → 20%,而非按 input 全算
    expect(cacheHitRate(2000, 8000)).toBeCloseTo(20)
    expect(cacheHitRate(600, 400)).toBeCloseTo(60)
  })
  it('无数据返回 null', () => {
    expect(cacheHitRate(0, 0)).toBeNull()
    expect(cacheHitRate(-5, 0)).toBeNull()
  })
})
