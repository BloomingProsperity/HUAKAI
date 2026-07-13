import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatCard } from '../../ui/StatCard'
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
  fmtInt,
  mapHealthScoreRows,
  mapLeaderboardRows,
  mapOverviewStats,
  mapPerfBucketRows,
  mapPerfMetricStats,
  mapPerformanceRows,
  mapProviderAccountRows,
  sparklinePoints,
  windowToRange,
  type HealthScoreTableRow,
  type LeaderboardTableRow,
  type PerfBucketTableRow,
  type PerformanceTableRow,
  type ProviderAccountTableRow,
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
  const overviewStats = mapOverviewStats(overview)
  const perfStats = mapPerfMetricStats(perf)
  const leaderboardRows = mapLeaderboardRows(board?.entries ?? [])
  const healthRows = health ? mapHealthScoreRows(health) : []
  const performanceRows = mapPerformanceRows(perfRank?.entries ?? [])
  const bucketRows = mapPerfBucketRows(bucket?.entries ?? [])
  const providerAccountRows = mapProviderAccountRows(paCounts?.counts ?? [])

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
        <section aria-label="运维总览统计" style={overviewStatsGridStyle}>
          {overviewStats.map((stat) => (
            <StatCard key={stat.label} label={stat.label} value={stat.value} hint={stat.hint} tone={stat.tone} />
          ))}
        </section>
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
          <section aria-label="性能分位统计" style={perfStatsGridStyle}>
            {perfStats.map((stat) => (
              <StatCard key={stat.label} label={stat.label} value={stat.value} tone={stat.tone} />
            ))}
          </section>
        )}
      </Card>

      {/* 模型成本排行 */}
      <Card title="模型成本排行">
        {errBoard ? (
          <Banner>{errBoard}</Banner>
        ) : !board ? (
          <EmptyState title="正在加载模型成本排行" hint="请稍候。" />
        ) : leaderboardRows.length === 0 ? (
          <EmptyState title="暂无模型成本数据" hint="窗口内产生真实模型调用后会显示成本、Token 与请求排行。" />
        ) : (
          <DataListTable label="模型成本排行榜" rows={leaderboardRows} rowKey={(row) => row.rank} columns={leaderboardColumns} />
        )}
      </Card>

      {/* 用量 + 渠道健康综合分 */}
      <Card title="健康分(用量业务面 + 渠道基础设施面)">
        {errHealth ? (
          <Banner>{errHealth}</Banner>
        ) : !health ? (
          <EmptyState title="正在加载健康分" hint="请稍候。" />
        ) : (
          <>
            <DataListTable label="平台健康分与观测信号" rows={healthRows} rowKey={(row) => row.id} columns={healthColumns} />
            <p style={{ margin: 0, fontSize: 11, color: 'var(--hk-ink-300)' }}>
              {health.signals.channel_health_available
                ? `渠道健康:可服务 ${fmtInt(health.signals.healthy_channels)} / 托管 ${fmtInt(health.signals.managed_channels)}`
                : '渠道健康信号未接入(平台级总览不绑租户),基础设施面按保守满分计。'}
            </p>
          </>
        )}
      </Card>

      {/* 性能排行(按模型 / Provider 账号) */}
      <Card title="性能排行(延迟 / 吞吐 / 错误率)">
        <Segmented options={PERF_DIMENSIONS} value={perfBy} onChange={setPerfBy} />
        {errPerfRank ? (
          <Banner>{errPerfRank}</Banner>
        ) : !perfRank ? (
          <EmptyState title="正在加载性能排行" hint="请稍候。" />
        ) : performanceRows.length === 0 ? (
          <EmptyState title="暂无性能排行数据" hint="窗口内产生真实调用后会显示延迟、吞吐与错误率排行。" />
        ) : (
          <DataListTable
            label={perfBy === 'model' ? '模型性能排行榜' : 'Provider 账号性能排行榜'}
            rows={performanceRows}
            rowKey={(row) => row.rank}
            columns={performanceColumns(perfBy === 'model' ? '模型' : 'Provider 账号')}
          />
        )}
      </Card>

      {/* 按时间桶的性能分布 */}
      <Card title="性能分桶(按时间)">
        <Segmented options={PERF_BUCKETS} value={bucketGran} onChange={setBucketGran} />
        {errBucket ? (
          <Banner>{errBucket}</Banner>
        ) : !bucket ? (
          <EmptyState title="正在加载性能分桶" hint="请稍候。" />
        ) : bucketRows.length === 0 ? (
          <EmptyState title="暂无性能分桶数据" hint="窗口内产生真实调用后会按所选时间粒度显示聚合性能。" />
        ) : (
          <DataListTable label="按时间聚合的性能分桶" rows={bucketRows} rowKey={(row) => row.id} columns={bucketColumns} />
        )}
      </Card>

      {/* 各 Provider 账号用量分布 */}
      <Card title="Provider 账号用量分布(请求 / Token / 费用)">
        {errPaCounts ? (
          <Banner>{errPaCounts}</Banner>
        ) : !paCounts ? (
          <EmptyState title="正在加载 Provider 账号分布" hint="请稍候。" />
        ) : providerAccountRows.length === 0 ? (
          <EmptyState title="暂无 Provider 账号用量" hint="窗口内账号产生真实调用后会显示请求、Token 与费用分布。" />
        ) : (
          <DataListTable label="Provider 账号用量分布" rows={providerAccountRows} rowKey={(row) => row.id} columns={providerAccountColumns} />
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

const leaderboardColumns: DataListColumn<LeaderboardTableRow>[] = [
  { key: 'rank', label: '#', render: (row) => <span className="hk-mono">{row.rank}</span> },
  { key: 'model', label: '模型', render: (row) => <code style={codeStyle}>{row.model}</code> },
  { key: 'cost', label: '成本', render: (row) => <span className="hk-mono">{row.cost}</span> },
  { key: 'tokens', label: 'Token', render: (row) => <span className="hk-mono">{row.tokens}</span> },
  { key: 'requests', label: '请求数', render: (row) => <span className="hk-mono">{row.requests}</span> },
]

const healthColumns: DataListColumn<HealthScoreTableRow>[] = [
  { key: 'metric', label: '指标', render: (row) => row.metric },
  { key: 'value', label: '当前值', render: (row) => <span className="hk-mono">{row.value}</span> },
  { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.statusText}</StatusBadge> },
]

function performanceColumns(dimensionLabel: string): DataListColumn<PerformanceTableRow>[] {
  return [
    { key: 'rank', label: '#', render: (row) => <span className="hk-mono">{row.rank}</span> },
    { key: 'dimension', label: dimensionLabel, render: (row) => <code style={codeStyle}>{row.key}</code> },
    { key: 'ttft', label: '平均 TTFT', render: (row) => <span className="hk-mono">{row.avgTtft}</span> },
    { key: 'tps', label: '平均 TPS', render: (row) => <span className="hk-mono">{row.avgTps}</span> },
    { key: 'requests', label: '请求数', render: (row) => <span className="hk-mono">{row.requests}</span> },
    { key: 'error-rate', label: '错误率', render: (row) => <span className="hk-mono">{row.errorRate}</span> },
  ]
}

const bucketColumns: DataListColumn<PerfBucketTableRow>[] = [
  { key: 'bucket', label: '时间桶', render: (row) => <span className="hk-mono">{row.bucket}</span> },
  { key: 'model', label: '模型', render: (row) => <code style={codeStyle}>{row.model}</code> },
  { key: 'ttft', label: '平均 TTFT', render: (row) => <span className="hk-mono">{row.avgTtft}</span> },
  { key: 'tps', label: '平均 TPS', render: (row) => <span className="hk-mono">{row.avgTps}</span> },
  { key: 'requests', label: '请求数', render: (row) => <span className="hk-mono">{row.requests}</span> },
  { key: 'errors', label: '错误数', render: (row) => <span className="hk-mono">{row.errors}</span> },
  { key: 'error-rate', label: '错误率', render: (row) => <span className="hk-mono">{row.errorRate}</span> },
]

const providerAccountColumns: DataListColumn<ProviderAccountTableRow>[] = [
  { key: 'account', label: '账号 ID', render: (row) => <span className="hk-mono">{row.account}</span> },
  { key: 'requests', label: '请求数', render: (row) => <span className="hk-mono">{row.requests}</span> },
  { key: 'input-tokens', label: '输入 Token', render: (row) => <span className="hk-mono">{row.inputTokens}</span> },
  { key: 'output-tokens', label: '输出 Token', render: (row) => <span className="hk-mono">{row.outputTokens}</span> },
  { key: 'tokens', label: '合计 Token', render: (row) => <span className="hk-mono">{row.tokens}</span> },
  { key: 'cost', label: '费用', render: (row) => <span className="hk-mono">{row.cost}</span> },
]

const overviewStatsGridStyle = { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 'var(--hk-space-3)' }
const perfStatsGridStyle = { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 'var(--hk-space-3)' }
const codeStyle = { fontSize: 12, color: 'var(--hk-ink-900)' }
