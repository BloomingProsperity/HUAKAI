import { describe, expect, it } from 'vitest'
import {
  buildMonthGrid,
  formatUsd,
  isValidMonth,
  monthOf,
  rewardRangeText,
  shiftMonth,
  totalCheckins,
  totalRewardCents,
} from './calendar'
import type { CheckinRecord } from './types'

describe('isValidMonth', () => {
  it('严格校验 YYYY-MM,月份须 01-12', () => {
    expect(isValidMonth('2026-06')).toBe(true)
    expect(isValidMonth('2026-01')).toBe(true)
    expect(isValidMonth('2026-12')).toBe(true)
    // 判别核心:月份越界必须拒。变异(放宽到 <=13)→ "2026-13" 误判合法,本断言 RED。
    expect(isValidMonth('2026-13')).toBe(false)
    expect(isValidMonth('2026-00')).toBe(false)
    expect(isValidMonth('2026-6')).toBe(false)
    expect(isValidMonth('not-a-month')).toBe(false)
  })
})

describe('monthOf', () => {
  it('按 UTC 取月份串(不受本地时区影响)', () => {
    // UTC 跨日边界:此刻 UTC 已是 7 月 1 号。
    expect(monthOf(new Date('2026-07-01T00:30:00Z'))).toBe('2026-07')
  })
})

describe('shiftMonth', () => {
  it('跨年进位/退位正确', () => {
    expect(shiftMonth('2026-12', 1)).toBe('2027-01')
    expect(shiftMonth('2026-01', -1)).toBe('2025-12')
    expect(shiftMonth('2026-06', 1)).toBe('2026-07')
  })
  it('非法月份原样返回', () => {
    expect(shiftMonth('bad', 1)).toBe('bad')
  })
})

describe('buildMonthGrid', () => {
  const records: CheckinRecord[] = [
    { checkin_date: '2026-06-06', reward_cents: 11, currency_code: 'USD', created_at: '2026-06-06T00:00:00Z' },
    { checkin_date: '2026-06-20', reward_cents: 9, currency_code: 'USD', created_at: '2026-06-20T00:00:00Z' },
  ]

  it('本月真实日数正确,且前导占位补齐首周', () => {
    // 2026-06-01 是周一(UTC),周日为首列 → 前导 1 个占位格。
    const grid = buildMonthGrid('2026-06', records)
    const placeholders = grid.filter((c) => !c.inMonth)
    const realDays = grid.filter((c) => c.inMonth)
    expect(placeholders.length).toBe(1)
    expect(realDays.length).toBe(30) // 六月 30 天
    expect(realDays[0].day).toBe(1)
    expect(realDays[realDays.length - 1].day).toBe(30)
  })

  it('命中记录的日期标记已签并带奖励额', () => {
    const grid = buildMonthGrid('2026-06', records)
    const d6 = grid.find((c) => c.date === '2026-06-06')!
    const d7 = grid.find((c) => c.date === '2026-06-07')!
    // 判别核心:有记录的日期 checkedIn=true 且 rewardCents 带出。
    // 变异(checkedIn 恒 false)→ 第一断言 RED;变异(rewardCents 恒 0)→ 第二断言 RED。
    expect(d6.checkedIn).toBe(true)
    expect(d6.rewardCents).toBe(11)
    expect(d7.checkedIn).toBe(false)
    expect(d7.rewardCents).toBe(0)
  })

  it('非法月份返回空网格', () => {
    expect(buildMonthGrid('2026-13', records)).toEqual([])
  })
})

describe('totalCheckins / totalRewardCents', () => {
  const records: CheckinRecord[] = [
    { checkin_date: '2026-06-06', reward_cents: 11, currency_code: 'USD', created_at: 'x' },
    { checkin_date: '2026-06-20', reward_cents: 9, currency_code: 'USD', created_at: 'x' },
  ]
  it('计数与求和', () => {
    expect(totalCheckins(records)).toBe(2)
    // 判别核心:奖励求和。变异(只取首条/恒 0)→ 本断言 RED。
    expect(totalRewardCents(records)).toBe(20)
    expect(totalRewardCents([])).toBe(0)
  })
})

describe('formatUsd', () => {
  it('cents 换算成美元两位小数', () => {
    expect(formatUsd(0)).toBe('$0.00')
    // 判别核心:整数部分按 100 进位、余数补零。变异(漏 /100 或漏补零)→ 这些断言 RED。
    expect(formatUsd(5)).toBe('$0.05')
    expect(formatUsd(123)).toBe('$1.23')
    expect(formatUsd(2000)).toBe('$20.00')
    expect(formatUsd(-150)).toBe('-$1.50')
  })
})

describe('rewardRangeText', () => {
  it('区间随机/固定/未配置 三态文案', () => {
    expect(rewardRangeText({ min_cents: 5, max_cents: 20 })).toBe('每日签到随机返还 $0.05 ~ $0.20 到账户余额')
    // 判别核心:min==max 走固定额分支。变异(总走随机分支)→ 本断言 RED。
    expect(rewardRangeText({ min_cents: 10, max_cents: 10 })).toBe('每日签到固定返还 $0.10 到账户余额')
    expect(rewardRangeText({ min_cents: 0, max_cents: 0 })).toBe('签到奖励区间暂未配置')
    expect(rewardRangeText({ min_cents: 30, max_cents: 10 })).toBe('签到奖励区间暂未配置')
  })
})
