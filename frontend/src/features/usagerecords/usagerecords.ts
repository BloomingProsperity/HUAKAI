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
