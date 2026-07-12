/*
 * 用量/缓存"日历热力网格"的纯逻辑(可单测)。GitHub 贡献图风:每天一个小方格,
 * 颜色深浅分 5 档(0=无用量,1..4 递增)。time-series 桶是"每天每模型"一条,
 * 先按天聚合,再算强度分档,并给出网格坐标(周为列、周一起的星期为行)。
 */

import type { KeyUsageTimeSeriesPoint } from './types'

export interface HeatCell {
  /** YYYY-MM-DD */
  day: string
  /** 当天聚合值(费用或缓存 token,取决于 pick)。 */
  value: number
  /** 强度分档 0..4。 */
  level: number
  /** 网格行:周一=0 … 周日=6。 */
  row: number
  /** 网格列:相对最早一天所在周的第几周(从 0 起)。 */
  col: number
}

export interface HeatGrid {
  cells: HeatCell[]
  max: number
  columns: number
}

/** 值 → 强度分档 0..4;value≤0 或 max≤0 为 0,否则 ceil(value/max*4) 钳制 [1,4]。 */
export function heatLevel(value: number, max: number): number {
  if (!Number.isFinite(value) || value <= 0 || !Number.isFinite(max) || max <= 0) return 0
  return Math.min(4, Math.max(1, Math.ceil((value / max) * 4)))
}

/** 周一起算的星期序:周一=0 … 周日=6(JS getUTCDay 周日=0)。 */
export function weekdayMonFirst(dayISO: string): number {
  const t = Date.parse(`${dayISO}T00:00:00Z`)
  if (!Number.isFinite(t)) return 0
  return (new Date(t).getUTCDay() + 6) % 7
}

const DAY_MS = 86400000

/** 把某天对齐到其所在周(周一)的 UTC 毫秒。 */
function mondayOf(dayISO: string): number {
  const t = Date.parse(`${dayISO}T00:00:00Z`)
  if (!Number.isFinite(t)) return NaN
  return t - weekdayMonFirst(dayISO) * DAY_MS
}

/**
 * 把 time-series 桶按天聚合成热力网格。pick 从单桶取该视图的值(费用 or 缓存 token)。
 * 网格列=从最早一天所在周起的周序,行=周一起星期序;强度按全网格最大值分档。
 */
export function buildHeatGrid(
  points: KeyUsageTimeSeriesPoint[],
  pick: (p: KeyUsageTimeSeriesPoint) => number,
): HeatGrid {
  const byDay = new Map<string, number>()
  for (const p of points) {
    const day = (p.day || '').slice(0, 10)
    if (!day) continue
    const v = pick(p)
    byDay.set(day, (byDay.get(day) ?? 0) + (Number.isFinite(v) && v > 0 ? v : 0))
  }
  const days = [...byDay.keys()].sort()
  if (days.length === 0) return { cells: [], max: 0, columns: 0 }

  const max = Math.max(...days.map((d) => byDay.get(d) ?? 0))
  const baseMonday = mondayOf(days[0])
  let columns = 0
  const cells: HeatCell[] = days.map((day) => {
    const value = byDay.get(day) ?? 0
    const col = Number.isFinite(baseMonday) ? Math.max(0, Math.round((mondayOf(day) - baseMonday) / (7 * DAY_MS))) : 0
    if (col + 1 > columns) columns = col + 1
    return { day, value, level: heatLevel(value, max), row: weekdayMonFirst(day), col }
  })
  return { cells, max, columns }
}

/** 单桶费用(定点字符串 → number)。 */
export function pickCost(p: KeyUsageTimeSeriesPoint): number {
  const n = Number(String(p.total_cost).trim())
  return Number.isFinite(n) ? n : 0
}

/** 单桶缓存 token(读+写)。 */
export function pickCache(p: KeyUsageTimeSeriesPoint): number {
  const r = p.tokens?.cache_read ?? 0
  const c = p.tokens?.cache_creation ?? 0
  return (Number.isFinite(r) ? r : 0) + (Number.isFinite(c) ? c : 0)
}
