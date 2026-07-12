import type { KeyUsageTimeSeriesPoint } from './types'

const DAY_MS = 24 * 60 * 60 * 1000
const MAX_WINDOW_DAYS = 31

export interface KeyAnalyticsWindow {
  from: string
  to: string
}

function parseUTCDay(value: string): Date | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return null
  const date = new Date(`${value}T00:00:00.000Z`)
  if (Number.isNaN(date.getTime()) || date.toISOString().slice(0, 10) !== value) return null
  return date
}

function dayString(date: Date): string {
  return date.toISOString().slice(0, 10)
}

/** 默认最近 30 个完整 UTC 日，右界为明日零点。 */
export function defaultKeyAnalyticsRange(now = new Date()): { fromDay: string; toDay: string } {
  const today = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()))
  const from = new Date(today.getTime() - 29 * DAY_MS)
  return { fromDay: dayString(from), toDay: dayString(today) }
}

/** 日期控件值转后端 RFC3339 半开窗口，并守住 31 天上限。 */
export function buildKeyAnalyticsWindow(
  fromDay: string,
  toDay: string,
): { ok: true; value: KeyAnalyticsWindow } | { ok: false; error: string } {
  const from = parseUTCDay(fromDay)
  const toInclusive = parseUTCDay(toDay)
  if (!from || !toInclusive) return { ok: false, error: '请选择有效的开始与结束日期' }
  const to = new Date(toInclusive.getTime() + DAY_MS)
  const duration = to.getTime() - from.getTime()
  if (duration <= 0) return { ok: false, error: '开始日期不能晚于结束日期' }
  if (duration > MAX_WINDOW_DAYS * DAY_MS) {
    return { ok: false, error: 'Key 级时间范围不能超过 31 天' }
  }
  return { ok: true, value: { from: from.toISOString(), to: to.toISOString() } }
}

/** 以当前序列最大费用为 100%，供 hk-bar 紧凑展示。 */
export function costBarPercent(
  point: KeyUsageTimeSeriesPoint,
  points: KeyUsageTimeSeriesPoint[],
): number {
  const costs = points.map((item) => Number(item.total_cost)).filter((value) => Number.isFinite(value) && value > 0)
  const max = costs.length > 0 ? Math.max(...costs) : 0
  const current = Number(point.total_cost)
  if (!Number.isFinite(current) || current <= 0 || max <= 0) return 0
  return Math.max(2, Math.min(100, (current / max) * 100))
}

export function aggregateTokenCount(point: KeyUsageTimeSeriesPoint): number {
  return point.tokens.input + point.tokens.output + point.tokens.cache_read + point.tokens.cache_creation
}
