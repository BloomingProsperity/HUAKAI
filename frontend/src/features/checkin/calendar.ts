import type { CheckinRecord, CheckinStatus } from './types'

/*
 * 每日签到纯逻辑:月份字符串构造/校验、本月签到日历网格、金额格式化、奖励区间文案。
 * 全部为无副作用纯函数,便于单测;UI 层只负责渲染与请求编排。
 *
 * 时区约定:后端的「日」与「月」均为 UTC(checkin_date / month 都是 UTC 历法日)。
 * 为避免本地时区把日历错位一天,这里一律按 UTC 解析与生成。
 */

/** YYYY-MM 月份串校验(严格四位年 + 两位月,月在 01-12)。 */
export function isValidMonth(month: string): boolean {
  const m = /^(\d{4})-(\d{2})$/.exec(month)
  if (!m) return false
  const mm = Number(m[2])
  // 判别核心:月份必须落在 1-12。变异(放宽到 <=13 等)→ "2026-13" 被误判合法,本断言 RED。
  return mm >= 1 && mm <= 12
}

/** 取某个 Date 对应的 UTC 月份串 YYYY-MM(用于"本月"默认值)。 */
export function monthOf(date: Date): string {
  const y = date.getUTCFullYear()
  const m = date.getUTCMonth() + 1
  return `${y}-${pad2(m)}`
}

/** 相邻月份导航:delta=-1 上一月、+1 下一月,跨年正确进位。入参非法则原样返回。 */
export function shiftMonth(month: string, delta: number): string {
  if (!isValidMonth(month)) return month
  const [y, m] = month.split('-').map(Number)
  // 用 0 基月做加减,再借 Date 归一化进位/退位(UTC 历法)。
  const d = new Date(Date.UTC(y, m - 1 + delta, 1))
  return monthOf(d)
}

/** 一个日历格子。inMonth=false 表示用于补齐首周的前导占位(非本月)。 */
export interface CalendarCell {
  /** YYYY-MM-DD;占位格为空串。 */
  date: string
  /** 月内第几天(1-31);占位格为 0。 */
  day: number
  /** 是否本月真实日期(占位补格为 false)。 */
  inMonth: boolean
  /** 该日是否已签到。 */
  checkedIn: boolean
  /** 该日返还奖励(cents);未签到为 0。 */
  rewardCents: number
}

/**
 * 构造本月签到日历网格。返回从"本月 1 号所在周的周日"起、按 7 列对齐的格子序列,
 * 首部用占位格补齐到周日,使日历整齐成行;records 中命中的日期标记 checkedIn。
 * 周首固定为周日(getUTCDay 0=周日)。month 非法时返回空数组。
 */
export function buildMonthGrid(month: string, records: CheckinRecord[]): CalendarCell[] {
  if (!isValidMonth(month)) return []
  const [y, m] = month.split('-').map(Number)
  const rewardByDate = indexRewards(records)
  // 本月天数:下个月 0 号 = 本月最后一天。
  const daysInMonth = new Date(Date.UTC(y, m, 0)).getUTCDate()
  const firstWeekday = new Date(Date.UTC(y, m - 1, 1)).getUTCDay() // 0=周日

  const cells: CalendarCell[] = []
  // 前导占位:把 1 号推到它真实的星期列。
  for (let i = 0; i < firstWeekday; i++) {
    cells.push({ date: '', day: 0, inMonth: false, checkedIn: false, rewardCents: 0 })
  }
  for (let day = 1; day <= daysInMonth; day++) {
    const date = `${y}-${pad2(m)}-${pad2(day)}`
    const reward = rewardByDate.get(date)
    cells.push({
      date,
      day,
      inMonth: true,
      // 判别核心:该日存在记录即已签到。变异(恒 false)→ 已签日不高亮,命中断言 RED。
      checkedIn: reward !== undefined,
      rewardCents: reward ?? 0,
    })
  }
  return cells
}

/** 把记录按 checkin_date 索引出奖励额(同日去重取首条)。 */
function indexRewards(records: CheckinRecord[]): Map<string, number> {
  const map = new Map<string, number>()
  for (const r of records) {
    if (!map.has(r.checkin_date)) map.set(r.checkin_date, r.reward_cents)
  }
  return map
}

/** 本月累计签到次数(记录条数)。 */
export function totalCheckins(records: CheckinRecord[]): number {
  return records.length
}

/** 本月累计返还金额(cents 求和)。 */
export function totalRewardCents(records: CheckinRecord[]): number {
  let sum = 0
  for (const r of records) sum += r.reward_cents
  return sum
}

/** cents → "$1.23" 文案(两位小数,负数也安全)。 */
export function formatUsd(cents: number): string {
  const sign = cents < 0 ? '-' : ''
  const abs = Math.abs(cents)
  const dollars = Math.floor(abs / 100)
  const rem = abs % 100
  return `${sign}$${dollars}.${pad2(rem)}`
}

/**
 * 奖励区间说明文案。后端每次签到在 [min,max] 间随机返还。
 * 区间退化(min==max)时只说固定额。status 缺配时给兜底文案。
 */
export function rewardRangeText(status: Pick<CheckinStatus, 'min_cents' | 'max_cents'>): string {
  const { min_cents, max_cents } = status
  if (min_cents <= 0 || max_cents <= 0 || min_cents > max_cents) {
    return '签到奖励区间暂未配置'
  }
  if (min_cents === max_cents) {
    return `每日签到固定返还 ${formatUsd(min_cents)} 到账户余额`
  }
  return `每日签到随机返还 ${formatUsd(min_cents)} ~ ${formatUsd(max_cents)} 到账户余额`
}

/** 左补零到两位。 */
function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}
