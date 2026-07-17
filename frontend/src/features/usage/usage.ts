import type { ApiKeyView } from '../keys/types'
import type { KeyUsageSummary } from './types'

export interface KeyUsageSourceRow {
  key: ApiKeyView
  summary: KeyUsageSummary | null
}

export interface KeyUsageTableRow {
  id: number
  name: string
  prefix: string
  cost: string
  requests: string
  inputTokens: string
  outputTokens: string
  cacheTokens: string
  available: boolean
}

export interface UsageStatView {
  label: string
  value: string
  hint: string
}

function formatCount(value: number): string {
  return Math.trunc(value).toLocaleString('en-US')
}

/** Key 汇总响应到六列表格行的纯映射；失败行保留 Key 身份并显式标记不可用。 */
export function mapKeyUsageRows(rows: KeyUsageSourceRow[]): KeyUsageTableRow[] {
  return rows.map(({ key, summary }) => ({
    id: key.api_key_id,
    name: key.name,
    prefix: key.key_prefix,
    cost: summary?.total_cost ?? '—',
    requests: summary ? formatCount(summary.request_count) : '—',
    inputTokens: summary ? formatCount(summary.total_tokens_input) : '—',
    outputTokens: summary ? formatCount(summary.total_tokens_output) : '—',
    cacheTokens: summary
      ? `${formatCount(summary.total_cache_read_tokens)}/${formatCount(summary.total_cache_creation_tokens)}`
      : '—',
    available: summary !== null,
  }))
}

/** 当前页 Key 汇总到三张统计卡；不可用汇总不冒充零值，但 Key 仍计入活跃数。 */
export function mapUsageStats(rows: KeyUsageSourceRow[], loading = false, unavailable = false): UsageStatView[] {
  if (loading && rows.length === 0) {
    return [
      { label: '活跃 Key 数', value: '…', hint: '当前页正在加载' },
      { label: '合计花费', value: '…', hint: '当前页 USD' },
      { label: '合计请求', value: '…', hint: '当前页已取得汇总' },
    ]
  }
  if (unavailable && rows.length === 0) {
    return [
      { label: '活跃 Key 数', value: '—', hint: '当前页数据暂不可用' },
      { label: '合计花费', value: '—', hint: '当前页 USD 暂不可用' },
      { label: '合计请求', value: '—', hint: '当前页数据暂不可用' },
    ]
  }

  let totalCost = 0
  let totalRequests = 0
  let unavailableCount = 0
  for (const { summary } of rows) {
    if (!summary) {
      unavailableCount++
      continue
    }
    const cost = Number(summary.total_cost)
    if (Number.isFinite(cost)) totalCost += cost
    totalRequests += summary.request_count
  }
  const availabilityHint = unavailableCount > 0 ? `${unavailableCount} 个 Key 汇总暂不可用` : '当前页活跃 Key'
  return [
    { label: '活跃 Key 数', value: formatCount(rows.length), hint: availabilityHint },
    { label: '合计花费', value: `$${totalCost.toFixed(4)}`, hint: '当前页 USD' },
    { label: '合计请求', value: formatCount(totalRequests), hint: '当前页已取得汇总' },
  ]
}
