import type { UsageRecord, UsageTokens } from './types'

/*
 * 用量明细的纯逻辑(可单测):请求状态(end_class)→中文+配色、费用/token 格式化、游标分页。
 * status 镜像后端 usageStatus(handler.go:259):pending_reconciliation 待对账;
 * 成功类 end_class = stream_end_graceful / non_streaming;其余(upstream_error_5xx 等)= 失败。
 */

export type Tone = 'ok' | 'warn' | 'danger' | 'muted' | 'info'

/** 后端判定为「成功」的 end_class(与 ListUsageRecords 的 outcome=success 过滤一致)。 */
const SUCCESS_CLASSES = new Set(['stream_end_graceful', 'non_streaming'])

/** 状态配色:成功→ok;待对账→warn;空→muted;其余(各类错误)→danger。 */
export function statusTone(status: string): Tone {
  const v = status.trim()
  if (!v) return 'muted'
  if (v === 'pending_reconciliation') return 'warn'
  if (SUCCESS_CLASSES.has(v)) return 'ok'
  return 'danger'
}

/** 已知状态给中文标签;未知错误类原样回显(保留诊断信息不丢)。 */
const STATUS_LABELS: Record<string, string> = {
  pending_reconciliation: '待对账',
  stream_end_graceful: '成功',
  non_streaming: '成功',
}

export function statusLabel(status: string): string {
  const v = status.trim()
  if (!v) return '—'
  return STATUS_LABELS[v] ?? v
}

/** 是否成功状态(用于汇总成功率等场景)。 */
export function isSuccess(status: string): boolean {
  return SUCCESS_CLASSES.has(status.trim())
}

/**
 * 费用格式化:后端是定点小数串(如 "0.01000000")。转 USD 展示,去尾零但保留有效精度;
 * 极小额(<0.0001)用更多小数避免显示成 $0.00。非数字原样。
 */
export function formatCost(actualCost: string): string {
  const v = actualCost.trim()
  if (!v) return '—'
  const n = Number(v)
  if (!Number.isFinite(n)) return v
  if (n === 0) return '$0'
  // 非零但小于最小可展示精度(1e-6)时显示阈值形态,不塌成 $0(计费日志诚实性:
  // 真实发生过的微小扣费不能显示成 0 误导用户)。
  if (Math.abs(n) < 0.000001) return n > 0 ? '<$0.000001' : '>-$0.000001'
  // 选取能体现该值的小数位:大额 4 位足够,极小额放宽到 6 位。
  const decimals = Math.abs(n) >= 0.0001 ? 4 : 6
  const fixed = n.toFixed(decimals)
  // 去尾零(但保留至少 2 位小数,符合金额观感)。
  const trimmed = fixed.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '')
  const parts = trimmed.split('.')
  if (parts.length === 2 && parts[1].length === 1) {
    return `$${parts[0]}.${parts[1]}0`
  }
  return `$${trimmed}`
}

/** token 总数(input+output,不含 cache;cache 单列展示)。 */
export function totalTokens(tokens: UsageTokens): number {
  return (tokens.input || 0) + (tokens.output || 0)
}

/** token 简明摘要:"入 10 / 出 20"(+缓存创建/读,若有)。 */
export function tokensSummary(tokens: UsageTokens): string {
  let s = `入 ${tokens.input || 0} / 出 ${tokens.output || 0}`
  if (tokens.cache_creation) s += ` / 缓存写 ${tokens.cache_creation}`
  if (tokens.cache_read) s += ` / 缓存读 ${tokens.cache_read}`
  return s
}

/** 模型展示:优先 requested,上游不同则附注。 */
export function modelDisplay(record: Pick<UsageRecord, 'requested_model' | 'upstream_model'>): string {
  const req = record.requested_model.trim()
  const up = record.upstream_model.trim()
  if (!req) return up || '—'
  if (up && up !== req) return `${req} → ${up}`
  return req
}

/** 游标分页:next_cursor 非空即还有下一页。 */
export function hasMore(nextCursor: string): boolean {
  return nextCursor.trim() !== ''
}

// ── 用量 CSV 导出(GET /v1/me/usage/export.csv)的纯逻辑 ────────────────────────
// 后端 meexporthttp 经 exporthttp.ParseExportRange 强制 from/to 两个 RFC3339 参数:
//   ① 缺任一 → 400 from_required / to_required;
//   ② 非 RFC3339 → 400 *_invalid;
//   ③ from 晚于 to → 400 invalid_date_range;
//   ④ 跨度 > 366 天 → 400 date_range_too_large。
// 这里前端先行校验,避免无谓请求;并把日期选择器的「YYYY-MM-DD」转成后端要的 RFC3339。

/** 导出窗口最大跨度(对齐后端 maxExportWindow = 366 天)。 */
export const MAX_EXPORT_DAYS = 366

/**
 * 把 <input type="date"> 的 'YYYY-MM-DD' 转成当天 UTC 起点的 RFC3339(from)。
 * 非法/空串返回空串(由 buildExportQuery / validateExportRange 拦截)。
 */
export function dayStartRFC3339(day: string): string {
  const v = day.trim()
  if (!/^\d{4}-\d{2}-\d{2}$/.test(v)) return ''
  const d = new Date(`${v}T00:00:00.000Z`)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

/**
 * 把 'YYYY-MM-DD' 转成当天 UTC 终点(次日零点)的 RFC3339(to,半开区间右界)。
 * 这样选「同一天」也能覆盖整日数据,而非零跨度。
 */
export function dayEndRFC3339(day: string): string {
  const v = day.trim()
  if (!/^\d{4}-\d{2}-\d{2}$/.test(v)) return ''
  const d = new Date(`${v}T00:00:00.000Z`)
  if (Number.isNaN(d.getTime())) return ''
  d.setUTCDate(d.getUTCDate() + 1)
  return d.toISOString()
}

/**
 * 校验导出日期范围(输入为两个 'YYYY-MM-DD')。通过返回 null,否则返回中文错误。
 * 判别核心:from 晚于 to 必须拦下(变异成放行 → 后端 400 invalid_date_range → RED);
 *          跨度 > 366 天必须拦下(变异成放行 → 后端 400 date_range_too_large → RED)。
 */
export function validateExportRange(fromDay: string, toDay: string): string | null {
  const from = dayStartRFC3339(fromDay)
  const to = dayStartRFC3339(toDay)
  if (!from || !to) return '请选择有效的起止日期'
  const fromMs = Date.parse(from)
  const toMs = Date.parse(to)
  if (fromMs > toMs) return '开始日期不能晚于结束日期'
  // 含右界整日,跨度按天数计;> 366 天后端会拒。
  const days = Math.round((toMs - fromMs) / 86_400_000) + 1
  if (days > MAX_EXPORT_DAYS) return `导出范围不能超过 ${MAX_EXPORT_DAYS} 天`
  return null
}

/**
 * 构造导出查询参数:固定 format=csv,from/to 转成 RFC3339。
 * 判别核心:from 用当天起点、to 用次日零点(右界半开),且 format 必须为 csv
 * (变异成漏 from/to 或用错边界 → 后端 400 / 数据缺失 → RED)。
 */
export function buildExportQuery(fromDay: string, toDay: string): { format: string; from: string; to: string } {
  return { format: 'csv', from: dayStartRFC3339(fromDay), to: dayEndRFC3339(toDay) }
}

/** 默认导出范围:最近 N 天(含今天),返回 [fromDay, toDay] 的 'YYYY-MM-DD'。 */
export function defaultExportRange(days = 30, now: Date = new Date()): { fromDay: string; toDay: string } {
  const toDay = isoDay(now)
  const from = new Date(now)
  from.setUTCDate(from.getUTCDate() - (days - 1))
  return { fromDay: isoDay(from), toDay }
}

/** Date → 'YYYY-MM-DD'(UTC)。 */
function isoDay(d: Date): string {
  return d.toISOString().slice(0, 10)
}
