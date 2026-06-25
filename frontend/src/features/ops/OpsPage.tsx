import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getLeaderboard, getOverview, getPerfMetrics } from './api'
import { fmtInt, fmtLatencyMs, sparklinePoints, successRateTone } from './ops'
import { OPS_WINDOWS, type LeaderboardResponse, type OverviewResponse, type PerfMetricsResponse } from './types'

/*
 * Ops 运维大屏(运维台,P1)。三块只读观测:总览 KPI + 内联 SVG 请求趋势、性能分位(p50/p95/p99)、
 * 模型成本排行。各块独立加载,某端点失败只提示该块、不连累整页。数据来自 /v1/admin/usage/*。
 */
export function OpsPage() {
  const [window, setWindow] = useState('7d')
  const [overview, setOverview] = useState<OverviewResponse | null>(null)
  const [perf, setPerf] = useState<PerfMetricsResponse | null>(null)
  const [board, setBoard] = useState<LeaderboardResponse | null>(null)
  const [errOverview, setErrOverview] = useState<string | null>(null)
  const [errPerf, setErrPerf] = useState<string | null>(null)
  const [errBoard, setErrBoard] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    const { signal } = ctrl
    const errOf = (e: unknown) => (e instanceof ApiError ? `${e.message}(${e.code})` : '加载失败')
    setErrOverview(null)
    setErrPerf(null)
    setErrBoard(null)
    getOverview(window, signal).then(setOverview).catch((e) => !signal.aborted && setErrOverview(errOf(e)))
    getPerfMetrics(window, signal).then(setPerf).catch((e) => !signal.aborted && setErrPerf(errOf(e)))
    getLeaderboard(window, signal).then(setBoard).catch((e) => !signal.aborted && setErrBoard(errOf(e)))
    return () => ctrl.abort()
  }, [window])

  const trend = overview?.trend ?? []
  const trendValues = trend.map((p) => p.requests)
  const points = sparklinePoints(trendValues, 600, 80, 4)

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>运维大屏</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>请求量 / 成本 / 成功率 / 延迟分位 / 模型排行 —— 只读观测。</p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
          {OPS_WINDOWS.map((w) => (
            <button
              key={w.value}
              type="button"
              onClick={() => setWindow(w.value)}
              style={{
                height: 30,
                padding: '0 var(--hk-space-3)',
                border: `1px solid ${window === w.value ? 'var(--hk-primary-600)' : 'var(--hk-line)'}`,
                borderRadius: 'var(--hk-radius-md)',
                background: window === w.value ? 'var(--hk-primary-500)' : 'var(--hk-surface)',
                color: window === w.value ? '#fff' : 'var(--hk-ink-700)',
                fontSize: 13,
                cursor: 'pointer',
              }}
            >
              {w.label}
            </button>
          ))}
        </div>
      </header>

      {/* 总览 KPI */}
      {errOverview ? (
        <Banner>{errOverview}</Banner>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 'var(--hk-space-3)' }}>
          <Kpi label="请求数" value={overview ? fmtInt(overview.totals.requests) : '…'} />
          <Kpi label="总成本" value={overview ? `$${overview.totals.total_cost}` : '…'} mono />
          <Kpi label="总 Token" value={overview ? fmtInt(overview.totals.total_tokens) : '…'} />
          <Kpi label="活跃用户" value={overview ? fmtInt(overview.totals.active_users) : '…'} />
          <Kpi label="活跃 Key" value={overview ? fmtInt(overview.totals.active_api_keys) : '…'} />
          <Kpi
            label="成功率"
            value={overview ? `${overview.totals.success_rate}%` : '…'}
            badge={overview ? successRateTone(overview.totals.success_rate) : undefined}
          />
        </div>
      )}

      {/* 请求趋势(内联 SVG) */}
      {overview && trendValues.length > 1 && (
        <Card title="请求趋势(按日)">
          <svg viewBox="0 0 600 80" width="100%" height="80" preserveAspectRatio="none" role="img" aria-label="请求趋势折线">
            <polyline points={points} fill="none" stroke="var(--hk-primary-500)" strokeWidth="2" vectorEffect="non-scaling-stroke" />
          </svg>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, color: 'var(--hk-ink-300)' }}>
            <span>{trend[0]?.day}</span>
            <span>{trend[trend.length - 1]?.day}</span>
          </div>
        </Card>
      )}

      {/* 性能分位 */}
      <Card title="性能(延迟分位 / 吞吐 / 错误率)">
        {errPerf ? (
          <Banner>{errPerf}</Banner>
        ) : (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 'var(--hk-space-3)' }}>
            <Kpi label="P50 延迟" value={perf ? fmtLatencyMs(perf.latency_percentiles_ms.p50) : '…'} mono small />
            <Kpi label="P95 延迟" value={perf ? fmtLatencyMs(perf.latency_percentiles_ms.p95) : '…'} mono small />
            <Kpi label="P99 延迟" value={perf ? fmtLatencyMs(perf.latency_percentiles_ms.p99) : '…'} mono small />
            <Kpi label="平均 TTFT" value={perf ? `${perf.summary.avg_ttft_ms}ms` : '…'} mono small />
            <Kpi label="平均 TPS" value={perf ? perf.summary.avg_tps : '…'} mono small />
            <Kpi label="错误率" value={perf ? `${perf.summary.error_rate}%` : '…'} mono small />
          </div>
        )}
      </Card>

      {/* 模型成本排行 */}
      <Card title="模型成本排行">
        {errBoard ? (
          <Banner>{errBoard}</Banner>
        ) : !board ? (
          <Empty>加载中…</Empty>
        ) : board.entries.length === 0 ? (
          <Empty>暂无数据。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['#', '模型', '成本', 'Token', '请求数'].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {board.entries.map((e) => (
                  <tr key={e.rank} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={tdMono}>{e.rank}</td>
                    <td style={td}>
                      <code style={{ fontSize: 12, color: 'var(--hk-ink-900)' }}>{e.key}</code>
                    </td>
                    <td style={tdMono}>${e.total_cost}</td>
                    <td style={tdMono}>{fmtInt(e.total_tokens)}</td>
                    <td style={tdMono}>{fmtInt(e.request_count)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  )
}

function Kpi({ label, value, mono, small, badge }: { label: string; value: string; mono?: boolean; small?: boolean; badge?: 'ok' | 'warn' | 'danger' }) {
  return (
    <div style={{ padding: 'var(--hk-space-4)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{label}</span>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
        <span style={{ fontSize: small ? 18 : 24, fontWeight: 700, color: 'var(--hk-ink-900)', fontFamily: mono ? 'var(--hk-font-mono)' : undefined, lineHeight: 1.1 }}>{value}</span>
        {badge && <StatusBadge tone={badge}>{badge === 'ok' ? '健康' : badge === 'warn' ? '注意' : '告警'}</StatusBadge>}
      </div>
    </div>
  )
}
function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', padding: 'var(--hk-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <h2 style={{ fontSize: 14, color: 'var(--hk-ink-700)' }}>{title}</h2>
      {children}
    </section>
  )
}
function Banner({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{children}</div>
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-6)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-2) var(--hk-space-3)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-2) var(--hk-space-3)', verticalAlign: 'middle' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
