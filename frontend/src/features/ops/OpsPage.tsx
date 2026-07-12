import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  getHealthScore,
  getLeaderboard,
  getOverview,
  getPerfByBucket,
  getPerfMetrics,
  getPerformance,
  getProviderAccountCounts,
} from './api'
import {
  fmtFractionPct,
  fmtInt,
  fmtLatencyMs,
  healthScoreTone,
  sparklinePoints,
  successRateTone,
  totalTokens,
  windowToRange,
} from './ops'
import {
  OPS_WINDOWS,
  PERF_BUCKETS,
  PERF_DIMENSIONS,
  type HealthScoreResponse,
  type LeaderboardResponse,
  type OverviewResponse,
  type PerfBucketGranularity,
  type PerfBucketResponse,
  type PerfDimension,
  type PerformanceResponse,
  type PerfMetricsResponse,
  type ProviderAccountCountsResponse,
} from './types'

/*
 * Ops 运维大屏(运维台,P1)。三块只读观测:总览 KPI + 内联 SVG 请求趋势、性能分位(p50/p95/p99)、
 * 模型成本排行。各块独立加载,某端点失败只提示该块、不连累整页。数据来自 /v1/admin/usage/*。
 */
export function OpsPage() {
  const [window, setWindow] = useState('7d')
  const [perfBy, setPerfBy] = useState<PerfDimension>('model')
  const [bucketGran, setBucketGran] = useState<PerfBucketGranularity>('hour')
  const [overview, setOverview] = useState<OverviewResponse | null>(null)
  const [perf, setPerf] = useState<PerfMetricsResponse | null>(null)
  const [board, setBoard] = useState<LeaderboardResponse | null>(null)
  const [perfRank, setPerfRank] = useState<PerformanceResponse | null>(null)
  const [bucket, setBucket] = useState<PerfBucketResponse | null>(null)
  const [health, setHealth] = useState<HealthScoreResponse | null>(null)
  const [paCounts, setPaCounts] = useState<ProviderAccountCountsResponse | null>(null)
  const [errOverview, setErrOverview] = useState<string | null>(null)
  const [errPerf, setErrPerf] = useState<string | null>(null)
  const [errBoard, setErrBoard] = useState<string | null>(null)
  const [errPerfRank, setErrPerfRank] = useState<string | null>(null)
  const [errBucket, setErrBucket] = useState<string | null>(null)
  const [errHealth, setErrHealth] = useState<string | null>(null)
  const [errPaCounts, setErrPaCounts] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    const { signal } = ctrl
    const errOf = (e: unknown) => (e instanceof ApiError ? `${e.message}(${e.code})` : '加载失败')
    setErrOverview(null)
    setErrPerf(null)
    setErrBoard(null)
    setErrHealth(null)
    setErrPaCounts(null)
    getOverview(window, signal).then(setOverview).catch((e) => !signal.aborted && setErrOverview(errOf(e)))
    getPerfMetrics(window, signal).then(setPerf).catch((e) => !signal.aborted && setErrPerf(errOf(e)))
    getLeaderboard(window, signal).then(setBoard).catch((e) => !signal.aborted && setErrBoard(errOf(e)))
    getHealthScore(window, signal).then(setHealth).catch((e) => !signal.aborted && setErrHealth(errOf(e)))
    // provider-account-counts 只收绝对 [from,to](RFC3339),由 window 折算;to=now。
    const { from, to } = windowToRange(window, new Date())
    getProviderAccountCounts(from, to, signal)
      .then(setPaCounts)
      .catch((e) => !signal.aborted && setErrPaCounts(errOf(e)))
    return () => ctrl.abort()
  }, [window])

  // 性能排行随 window + 维度(model / provider_account)切换重载。
  useEffect(() => {
    const ctrl = new AbortController()
    const { signal } = ctrl
    const errOf = (e: unknown) => (e instanceof ApiError ? `${e.message}(${e.code})` : '加载失败')
    setErrPerfRank(null)
    setPerfRank(null)
    getPerformance(perfBy, window, signal)
      .then(setPerfRank)
      .catch((e) => !signal.aborted && setErrPerfRank(errOf(e)))
    return () => ctrl.abort()
  }, [window, perfBy])

  // 分桶性能随 window + 粒度(hour / day)切换重载。
  useEffect(() => {
    const ctrl = new AbortController()
    const { signal } = ctrl
    const errOf = (e: unknown) => (e instanceof ApiError ? `${e.message}(${e.code})` : '加载失败')
    setErrBucket(null)
    setBucket(null)
    getPerfByBucket(bucketGran, window, signal)
      .then(setBucket)
      .catch((e) => !signal.aborted && setErrBucket(errOf(e)))
    return () => ctrl.abort()
  }, [window, bucketGran])

  const trend = overview?.trend ?? []
  const trendValues = trend.map((p) => p.requests)
  const points = sparklinePoints(trendValues, 600, 80, 4)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>运维大屏</h1>
          <p className="hk-sub">请求量 / 成本 / 成功率 / 延迟分位 / 模型排行 —— 只读观测。</p>
        </div>
        <div className="hk-seg">
          {OPS_WINDOWS.map((w) => (
            <button
              key={w.value}
              type="button"
              onClick={() => setWindow(w.value)}
              className={window === w.value ? 'is-on' : ''}
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
            // success_rate 是 0~1 小数(同 error_rate),须经 fmtFractionPct ×100 展示;原 `${x}%` 少算 100 倍。
            value={overview ? fmtFractionPct(overview.totals.success_rate) : '…'}
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
            {/* error_rate 是 0~1 小数(errorRateText=errorCount/requestCount StringFixed(4)),须 ×100 展示;原 `${x}%` 少算 100 倍。 */}
            <Kpi label="错误率" value={perf ? fmtFractionPct(perf.summary.error_rate) : '…'} mono small />
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
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['#', '模型', '成本', 'Token', '请求数'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {board.entries.map((e) => (
                  <tr key={e.rank}>
                    <td className="hk-mono">{e.rank}</td>
                    <td>
                      <code style={{ fontSize: 12, color: 'var(--hk-ink-900)' }}>{e.key}</code>
                    </td>
                    <td className="hk-mono">${e.total_cost}</td>
                    <td className="hk-mono">{fmtInt(e.total_tokens)}</td>
                    <td className="hk-mono">{fmtInt(e.request_count)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {/* 用量 + 渠道健康综合分 */}
      <Card title="健康分(用量业务面 + 渠道基础设施面)">
        {errHealth ? (
          <Banner>{errHealth}</Banner>
        ) : (
          <>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 'var(--hk-space-3)' }}>
              <Kpi
                label="综合分"
                value={health ? String(health.overall_score) : '…'}
                badge={health ? healthScoreTone(health.overall_score) : undefined}
              />
              <Kpi
                label="业务面"
                value={health ? String(health.business_score) : '…'}
                badge={health ? healthScoreTone(health.business_score) : undefined}
                small
              />
              <Kpi
                label="基础设施面"
                value={health ? String(health.infra_score) : '…'}
                badge={health ? healthScoreTone(health.infra_score) : undefined}
                small
              />
              <Kpi label="错误率" value={health ? fmtFractionPct(health.signals.error_rate) : '…'} mono small />
              <Kpi label="TTFT P99" value={health ? fmtLatencyMs(health.signals.ttft_p99_ms) : '…'} mono small />
            </div>
            {health && (
              <p style={{ margin: 0, fontSize: 11, color: 'var(--hk-ink-300)' }}>
                {health.signals.channel_health_available
                  ? `渠道健康:可服务 ${fmtInt(health.signals.healthy_channels)} / 托管 ${fmtInt(health.signals.managed_channels)}`
                  : '渠道健康信号未接入(平台级总览不绑租户),基础设施面按保守满分计。'}
              </p>
            )}
          </>
        )}
      </Card>

      {/* 性能排行(按模型 / Provider 账号) */}
      <Card title="性能排行(延迟 / 吞吐 / 错误率)">
        <Segmented options={PERF_DIMENSIONS} value={perfBy} onChange={setPerfBy} />
        {errPerfRank ? (
          <Banner>{errPerfRank}</Banner>
        ) : !perfRank ? (
          <Empty>加载中…</Empty>
        ) : perfRank.entries.length === 0 ? (
          <Empty>暂无数据。</Empty>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['#', perfBy === 'model' ? '模型' : 'Provider 账号', '平均 TTFT', '平均 TPS', '请求数', '错误率'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {perfRank.entries.map((e) => (
                  <tr key={e.rank}>
                    <td className="hk-mono">{e.rank}</td>
                    <td>
                      <code style={{ fontSize: 12, color: 'var(--hk-ink-900)' }}>{e.key || '—'}</code>
                    </td>
                    <td className="hk-mono">{e.avg_ttft_ms}ms</td>
                    <td className="hk-mono">{e.avg_tps}</td>
                    <td className="hk-mono">{fmtInt(e.request_count)}</td>
                    <td className="hk-mono">{fmtFractionPct(e.error_rate)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {/* 按时间桶的性能分布 */}
      <Card title="性能分桶(按时间)">
        <Segmented options={PERF_BUCKETS} value={bucketGran} onChange={setBucketGran} />
        {errBucket ? (
          <Banner>{errBucket}</Banner>
        ) : !bucket ? (
          <Empty>加载中…</Empty>
        ) : bucket.entries.length === 0 ? (
          <Empty>暂无数据。</Empty>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['时间桶', '模型', '平均 TTFT', '平均 TPS', '请求数', '错误数', '错误率'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {bucket.entries.map((e, i) => (
                  <tr key={`${e.bucket}-${e.key}-${i}`}>
                    <td className="hk-mono">{e.bucket}</td>
                    <td>
                      <code style={{ fontSize: 12, color: 'var(--hk-ink-900)' }}>{e.key || '—'}</code>
                    </td>
                    <td className="hk-mono">{e.avg_ttft_ms}ms</td>
                    <td className="hk-mono">{e.avg_tps}</td>
                    <td className="hk-mono">{fmtInt(e.request_count)}</td>
                    <td className="hk-mono">{fmtInt(e.error_count)}</td>
                    <td className="hk-mono">{fmtFractionPct(e.error_rate)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {/* 各 Provider 账号用量分布 */}
      <Card title="Provider 账号用量分布(请求 / Token / 费用)">
        {errPaCounts ? (
          <Banner>{errPaCounts}</Banner>
        ) : !paCounts ? (
          <Empty>加载中…</Empty>
        ) : paCounts.counts.length === 0 ? (
          <Empty>暂无数据。</Empty>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['账号 ID', '请求数', '输入 Token', '输出 Token', '合计 Token', '费用'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {paCounts.counts.map((c) => (
                  <tr key={c.provider_account_id}>
                    <td className="hk-mono">#{c.provider_account_id}</td>
                    <td className="hk-mono">{fmtInt(c.request_count)}</td>
                    <td className="hk-mono">{fmtInt(c.total_input_tokens)}</td>
                    <td className="hk-mono">{fmtInt(c.total_output_tokens)}</td>
                    <td className="hk-mono">{fmtInt(totalTokens(c))}</td>
                    <td className="hk-mono">${c.total_cost}</td>
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

// Segmented 是一个轻量分段切换器,用于性能维度(model/provider_account)与分桶粒度(hour/day)。
// 泛型 T 保证 onChange 回传的是受限字面量类型,避免在调用点丢类型。
function Segmented<T extends string>({
  options,
  value,
  onChange,
}: {
  options: ReadonlyArray<{ value: T; label: string }>
  value: T
  onChange: (v: T) => void
}) {
  return (
    <div className="hk-seg">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={value === o.value ? 'is-on' : ''}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

function Kpi({ label, value, mono, small, badge }: { label: string; value: string; mono?: boolean; small?: boolean; badge?: 'ok' | 'warn' | 'danger' }) {
  return (
    <div className="hk-metric">
      <div className="hk-metric__label">{label}</div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
        <span className={mono ? 'hk-metric__v hk-mono' : 'hk-metric__v'} style={small ? { fontSize: 18 } : undefined}>{value}</span>
        {badge && <StatusBadge tone={badge}>{badge === 'ok' ? '健康' : badge === 'warn' ? '注意' : '告警'}</StatusBadge>}
      </div>
    </div>
  )
}
function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>{title}</h3>
      </div>
      <div className="hk-card__body" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        {children}
      </div>
    </section>
  )
}
function Banner({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{children}</div>
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}
