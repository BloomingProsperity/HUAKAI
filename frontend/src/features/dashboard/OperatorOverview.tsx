import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMe } from '../../auth/me'
import { listEvents } from '../alerting/api'
import { DEFAULT_TENANT_ID } from '../alerting/alerting'
import type { AlertEvent } from '../alerting/types'
import { getHealthScore, getOverview, getPerfMetrics } from '../ops/api'
import { sparklinePoints } from '../ops/ops'
import { OPS_WINDOWS, type HealthScoreResponse, type OverviewResponse, type PerfMetricsResponse } from '../ops/types'
import { type MetricState } from './metrics'
import { alertRows, healthStatItem, overviewStatItems, perfStatItems, trendRequestValues, type StatItem } from './overview'

/*
 * 首页"经营总览"(仅运营台角色渲染):统计卡 + 请求趋势 + 最近告警。
 * 数据全部复用既有 admin 端点(/v1/admin/usage/* 与 alert-events),四路独立加载,
 * 任一失败只隐藏该块、不连累整页——与指标条同一降级哲学。
 */
export function OperatorOverview() {
  const me = useMe()
  // 告警按登录者所在租户查,未知(loading/degraded)才回退单租户默认;绝不硬编码。
  const tenantId = me.tenantId ?? DEFAULT_TENANT_ID
  const [win, setWin] = useState('24h')
  const [overview, setOverview] = useState<MetricState<OverviewResponse>>({ status: 'loading' })
  const [perf, setPerf] = useState<MetricState<PerfMetricsResponse>>({ status: 'loading' })
  const [health, setHealth] = useState<MetricState<HealthScoreResponse>>({ status: 'loading' })
  const [alerts, setAlerts] = useState<MetricState<AlertEvent[]>>({ status: 'loading' })

  useEffect(() => {
    const ctrl = new AbortController()
    const { signal } = ctrl
    const run = <R,>(p: Promise<R>, set: (s: MetricState<R>) => void) => {
      set({ status: 'loading' })
      p.then((v) => {
        if (!signal.aborted) set({ status: 'ok', value: v })
      }).catch(() => {
        if (!signal.aborted) set({ status: 'unavailable' })
      })
    }
    run(getOverview(win, signal), setOverview)
    run(getPerfMetrics(win, signal), setPerf)
    run(getHealthScore(win, signal), setHealth)
    return () => ctrl.abort()
  }, [win])

  // 告警区与时间窗口显式解耦:展示的是"最新 5 条"(端点无时间范围参数),只随租户变化重拉。
  useEffect(() => {
    const ctrl = new AbortController()
    const { signal } = ctrl
    setAlerts({ status: 'loading' })
    listEvents(tenantId, { limit: 5 }, signal)
      .then((r) => {
        if (!signal.aborted) setAlerts({ status: 'ok', value: r.items })
      })
      .catch(() => {
        if (!signal.aborted) setAlerts({ status: 'unavailable' })
      })
    return () => ctrl.abort()
  }, [tenantId])

  const stats: StatItem[] = [
    ...(overview.status === 'ok' ? overviewStatItems(overview.value) : []),
    ...(perf.status === 'ok' ? perfStatItems(perf.value) : []),
    ...(health.status === 'ok' ? [healthStatItem(health.value)] : []),
  ]
  const trend = overview.status === 'ok' ? trendRequestValues(overview.value.trend) : []
  const rows = alerts.status === 'ok' ? alertRows(alerts.value, 5) : []

  // 四路全不可用(非 admin token 等)时整段隐藏,普通用户首页保持原样。
  if (
    overview.status === 'unavailable' &&
    perf.status === 'unavailable' &&
    health.status === 'unavailable' &&
    alerts.status === 'unavailable'
  ) {
    return null
  }

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <h2 style={{ fontSize: 16, margin: 0 }}>经营总览</h2>
        <select value={win} onChange={(e) => setWin(e.target.value)} aria-label="总览时间窗口">
          {OPS_WINDOWS.map((w) => (
            <option key={w.value} value={w.value}>
              {w.label}
            </option>
          ))}
        </select>
      </div>
      {stats.length > 0 && (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))',
            gap: 'var(--hk-space-3)',
          }}
        >
          {stats.map((s) => (
            <div
              key={s.label}
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--hk-space-1)',
                padding: 'var(--hk-space-3)',
                background: 'var(--hk-surface)',
                border: '1px solid var(--hk-line)',
                borderRadius: 'var(--hk-radius-lg)',
              }}
            >
              <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{s.label}</span>
              <span style={{ fontSize: 20, fontWeight: 600, color: 'var(--hk-ink-900)' }}>{s.value}</span>
              <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{s.hint}</span>
            </div>
          ))}
        </div>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 2fr) minmax(0, 1fr)', gap: 'var(--hk-space-3)' }}>
        {trend.length > 1 && (
          <div
            style={{
              padding: 'var(--hk-space-3)',
              background: 'var(--hk-surface)',
              border: '1px solid var(--hk-line)',
              borderRadius: 'var(--hk-radius-lg)',
            }}
          >
            <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>请求趋势(按日)</span>
            <svg viewBox="0 0 240 56" width="100%" height="56" role="img" aria-label="请求趋势">
              <polyline
                points={sparklinePoints(trend, 240, 56)}
                fill="none"
                stroke="var(--hk-primary-600)"
                strokeWidth="2"
              />
            </svg>
          </div>
        )}
        <div
          style={{
            padding: 'var(--hk-space-3)',
            background: 'var(--hk-surface)',
            border: '1px solid var(--hk-line)',
            borderRadius: 'var(--hk-radius-lg)',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--hk-space-2)',
          }}
        >
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>最近告警</span>
            <Link to="/admin/alerting" style={{ fontSize: 12 }}>
              告警控制台 →
            </Link>
          </div>
          {alerts.status === 'loading' && <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>…</span>}
          {alerts.status === 'unavailable' && (
            <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>告警数据不可用</span>
          )}
          {alerts.status === 'ok' && rows.length === 0 && (
            <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>暂无告警</span>
          )}
          {rows.map((r) => (
            <span key={r.id} style={{ fontSize: 13, color: r.firing ? 'var(--hk-danger-600, #b42318)' : 'var(--hk-ink-700)' }}>
              {r.text}
            </span>
          ))}
        </div>
      </div>
    </section>
  )
}
