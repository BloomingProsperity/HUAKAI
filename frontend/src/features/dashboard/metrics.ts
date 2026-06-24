/*
 * Dashboard 指标纯逻辑(可单测)。指标条各卡独立加载,无权限端点(如 admin 账号)失败时降级显"—",
 * 不连累整页。这里抽出与 React 无关的状态映射与计数提取以便单测。
 */
export type MetricState<T> =
  | { status: 'loading' }
  | { status: 'ok'; value: T }
  | { status: 'unavailable' }

/** 加载态 → 展示串:加载中"…"、不可用(无权限/失败)"—"、成功走 fmt。 */
export function metricDisplay<T>(state: MetricState<T>, fmt: (v: T) => string): string {
  if (state.status === 'loading') return '…'
  if (state.status === 'unavailable') return '—'
  return fmt(state.value)
}

/**
 * 账号计数标签:游标分页只取首页,若还有下一页(next_cursor 非空)则以 "N+" 表示"至少 N 个",
 * 避免把首页条数误报成总数。
 */
export function accountCountLabel(resp: { items: unknown[]; page?: { next_cursor?: string | null } }): string {
  const n = resp.items.length
  return resp.page?.next_cursor ? `${n}+` : String(n)
}

/** Key 计数:后端直接给 count(全量计数),优先用它。 */
export function keyCount(resp: { count?: number; api_keys?: unknown[] }): number {
  return resp.count ?? resp.api_keys?.length ?? 0
}

/** 配额窗口数:有多少个计费窗口(日/周/月等)。 */
export function quotaWindowCount(resp: { items?: unknown[] }): number {
  return resp.items?.length ?? 0
}
