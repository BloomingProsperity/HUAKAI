/*
 * 经营总览纯逻辑(可单测):把 /v1/admin/usage/* 与告警事件的响应映射成首页统计卡与告警行。
 * 与 React 无关,便于变异测试咬住"卡片值算错/告警行取错字段"。
 */
import type { AlertEvent } from '../alerting/types'
import { fmtFractionPct, fmtInt, fmtLatencyMs } from '../ops/ops'
import type { HealthScoreResponse, OverviewResponse, OverviewTrendPoint, PerfMetricsResponse } from '../ops/types'

export interface StatItem {
  label: string
  value: string
  hint: string
}

/** 用量总览 → 6 张统计卡(请求/成功率/成本/Tokens/活跃用户/活跃 Key)。 */
export function overviewStatItems(o: OverviewResponse): StatItem[] {
  const t = o.totals
  return [
    { label: '请求数', value: fmtInt(t.requests), hint: `成功 ${fmtInt(t.success_count)} · 失败 ${fmtInt(t.error_count)}` },
    { label: '成功率', value: fmtFractionPct(t.success_rate), hint: '窗口内已结算请求' },
    { label: '成本', value: `$${t.total_cost}`, hint: '实际计费口径' },
    { label: 'Tokens', value: fmtInt(t.total_tokens), hint: '输入+输出合计' },
    { label: '活跃用户', value: fmtInt(t.active_users), hint: '窗口内有调用' },
    { label: '活跃 Key', value: fmtInt(t.active_api_keys), hint: '窗口内有调用' },
  ]
}

/** 性能摘要 → 2 张卡(p95 时延 / TTFT 均值)。 */
export function perfStatItems(p: PerfMetricsResponse): StatItem[] {
  return [
    { label: 'p95 时延', value: fmtLatencyMs(p.latency_percentiles_ms.p95), hint: `p99 ${fmtLatencyMs(p.latency_percentiles_ms.p99)}` },
    { label: '错误率', value: fmtFractionPct(p.summary.error_rate), hint: `错误 ${fmtInt(p.summary.error_count)} 次` },
  ]
}

/** 健康分 → 1 张卡;渠道信号可用时在 hint 里带上健康渠道比。 */
export function healthStatItem(h: HealthScoreResponse): StatItem {
  const hint = h.signals.channel_health_available
    ? `渠道 ${h.signals.healthy_channels}/${h.signals.managed_channels} 健康`
    : `业务 ${h.business_score} · 设施 ${h.infra_score}`
  return { label: '健康分', value: String(h.overall_score), hint }
}

/** 趋势图取请求序列;空趋势返回空数组(调用方隐藏图)。 */
export function trendRequestValues(trend: OverviewTrendPoint[]): number[] {
  return trend.map((p) => p.requests)
}

export interface AlertRow {
  id: number
  text: string
  firing: boolean
}

/** 告警事件 → 展示行:状态 + 规则号 + 触发时刻(本地时分)。 */
export function alertRows(events: AlertEvent[], limit: number): AlertRow[] {
  return events.slice(0, limit).map((e) => ({
    id: e.id,
    firing: e.state === 'firing',
    text: `${e.state === 'firing' ? '告警中' : '已恢复'} · 规则 #${e.rule_id} · ${formatFiredAt(e.fired_at)}`,
  }))
}

function formatFiredAt(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
