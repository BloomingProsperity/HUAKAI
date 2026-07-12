/*
 * 账号最近请求表的纯展示逻辑(可单测)。后端已把 status 归一为 success/error,
 * latency_ms 必有、ttft_ms 可空(首字节或上游起始时间缺失时为 null)。
 */
import type { AccountRecentRequestItem } from './types'

/** 延迟毫秒 → 文案;非有限值显示 "—"。 */
export function fmtLatency(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms)) return '—'
  return `${Math.max(0, Math.round(ms))} ms`
}

/** TTFT 可空:null → "—"(不把缺失当 0)。 */
export function fmtTtft(ms: number | null | undefined): string {
  return ms == null || !Number.isFinite(ms) ? '—' : `${Math.max(0, Math.round(ms))} ms`
}

/** 状态语气:success→ok,其余→danger(与全站 StatusBadge 一致)。 */
export function recentStatusTone(status: string): 'ok' | 'danger' {
  return status === 'success' ? 'ok' : 'danger'
}

/** 模型展示:上游模型与请求模型不同则显示 "req → upstream"。 */
export function recentModelDisplay(item: AccountRecentRequestItem): string {
  const req = item.model || '—'
  const up = item.upstream_model
  return up && up !== item.model ? `${req} → ${up}` : req
}
