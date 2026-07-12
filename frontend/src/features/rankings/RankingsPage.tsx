import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listRankings } from './api'
import {
  barRatio,
  DEFAULT_RANKINGS_LIMIT,
  formatCount,
  formatShare,
  LIMIT_CHOICES,
  metricLabel,
  metricValue,
  rankBy,
  type RankMetric,
} from './rankings'
import type { RankingEntry } from './types'

/*
 * 模型排行(公开榜页)。消费 GET /v1/public/rankings(公开无鉴权)。
 * 数据源=平台真实计费用量聚合(调用次数/总 token),非人造数据,故页脚标注来源。
 * 后端默认按「调用次数」降序;本页支持客户端切换指标(调用次数 / 总 Token / 调用占比)重排,
 * 并用相对最大值的条形可视化热度。纯只读对外门面。
 */
export function RankingsPage() {
  const [entries, setEntries] = useState<RankingEntry[]>([])
  const [scope, setScope] = useState<string>('platform')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [metric, setMetric] = useState<RankMetric>('request_count')
  const [limit, setLimit] = useState<number>(DEFAULT_RANKINGS_LIMIT)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    listRankings(limit, ctrl.signal)
      .then((data) => {
        setEntries(data.rankings ?? [])
        setScope(data.scope || 'platform')
      })
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载排行榜失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [limit])

  const ranked = useMemo(() => rankBy(entries, metric), [entries, metric])

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22, margin: 0 }}>模型排行</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          平台范围 · 按 {metricLabel(metric)} 排序。共 {entries.length} 个模型上榜。
        </p>
      </header>

      <div style={toolbar}>
        <Toggle
          options={[
            { v: 'request_count', l: '调用次数' },
            { v: 'token_total', l: '总 Token' },
            { v: 'request_share', l: '调用占比' },
          ]}
          value={metric}
          onChange={(v) => setMetric(v as RankMetric)}
        />
        <div style={{ flex: 1 }} />
        <label style={{ fontSize: 12, color: 'var(--hk-ink-500)', display: 'inline-flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
          展示
          <select value={limit} onChange={(e) => setLimit(Number(e.target.value))} style={sel}>
            {LIMIT_CHOICES.map((n) => (
              <option key={n} value={n}>
                前 {n}
              </option>
            ))}
          </select>
        </label>
      </div>

      {error && <div style={errorBox}>{error}</div>}

      {loading && entries.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : ranked.length === 0 ? (
        <Empty>暂无排行数据。平台尚未产生可统计的调用用量。</Empty>
      ) : (
        <div style={listCard}>
          {ranked.map((e) => (
            <RankRow key={e.model} entry={e} metric={metric} ratio={barRatio(e, ranked, metric)} />
          ))}
        </div>
      )}

      <p style={{ fontSize: 11, color: 'var(--hk-ink-300)', margin: 0 }}>
        数据源:平台计费用量聚合({scope})。名次随所选指标实时重排。
      </p>
    </div>
  )
}

function RankRow({ entry, metric, ratio }: { entry: RankingEntry; metric: RankMetric; ratio: number }) {
  // 主值=当前指标下的展示数值;副信息恒显示三项原始指标,便于对比。
  const main =
    metric === 'request_share'
      ? formatShare(entry.request_share)
      : formatCount(metricValue(entry, metric))
  return (
    <div style={row}>
      <div style={rankBadge(entry.rank)}>{entry.rank}</div>
      <div style={{ minWidth: 0, flex: '1 1 auto', display: 'flex', flexDirection: 'column', gap: 6 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', minWidth: 0 }}>
          <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 13, color: 'var(--hk-ink-900)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {entry.model || '(未命名)'}
          </code>
          {entry.rank <= 3 && <StatusBadge tone="ok">热门</StatusBadge>}
        </div>
        <div style={barTrack} aria-hidden>
          <div style={{ ...barFill, width: `${Math.round(ratio * 100)}%` }} />
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-4)', fontSize: 11, color: 'var(--hk-ink-500)' }}>
          <span>
            调用 <b style={metricNum}>{formatCount(entry.request_count)}</b>
          </span>
          <span>
            Token <b style={metricNum}>{formatCount(entry.token_total)}</b>
          </span>
          <span>
            占比 <b style={metricNum}>{formatShare(entry.request_share)}</b>
          </span>
        </div>
      </div>
      <div style={{ textAlign: 'right', flex: '0 0 auto', display: 'flex', flexDirection: 'column', alignItems: 'flex-end' }}>
        <span style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 16, color: 'var(--hk-ink-900)' }}>{main}</span>
        <span style={{ fontSize: 10, color: 'var(--hk-ink-300)' }}>{metricLabel(metric)}</span>
      </div>
    </div>
  )
}

function Toggle({ options, value, onChange }: { options: Array<{ v: string; l: string }>; value: string; onChange: (v: string) => void }) {
  return (
    <div style={{ display: 'inline-flex', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', overflow: 'hidden' }}>
      {options.map((o) => (
        <button
          key={o.v}
          type="button"
          onClick={() => onChange(o.v)}
          style={{
            height: 32,
            padding: '0 var(--hk-space-3)',
            fontSize: 12,
            cursor: 'pointer',
            border: 'none',
            background: value === o.v ? 'var(--hk-primary-500)' : 'var(--hk-surface)',
            color: value === o.v ? '#fff' : 'var(--hk-ink-700)',
          }}
        >
          {o.l}
        </button>
      ))}
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const toolbar: React.CSSProperties = {
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: 'var(--hk-space-2)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  padding: 'var(--hk-space-3)',
}
const sel: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-2)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
}
const listCard: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  overflow: 'hidden',
}
const row: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 'var(--hk-space-3)',
  padding: 'var(--hk-space-3) var(--hk-space-4)',
  borderTop: '1px solid var(--hk-line)',
}
function rankBadge(rank: number): React.CSSProperties {
  // 前三名用主色实底,其余用浅底,呼应「玉青·克制」。
  const top = rank <= 3
  return {
    flex: '0 0 auto',
    width: 28,
    height: 28,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    borderRadius: 'var(--hk-radius-pill)',
    fontSize: 13,
    fontWeight: 600,
    fontFamily: 'var(--hk-font-mono)',
    background: top ? 'var(--hk-primary-500)' : 'var(--hk-surface-sunken)',
    color: top ? '#fff' : 'var(--hk-ink-500)',
    border: top ? 'none' : '1px solid var(--hk-line)',
  }
}
const barTrack: React.CSSProperties = {
  height: 6,
  borderRadius: 'var(--hk-radius-pill)',
  background: 'var(--hk-surface-sunken)',
  overflow: 'hidden',
}
const barFill: React.CSSProperties = {
  height: '100%',
  background: 'var(--hk-primary-500)',
  borderRadius: 'var(--hk-radius-pill)',
  transition: 'width 160ms ease',
}
const metricNum: React.CSSProperties = { fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', fontWeight: 600 }
const errorBox: React.CSSProperties = {
  padding: 'var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  color: 'var(--hk-danger)',
  background: 'var(--hk-danger-soft)',
  border: '1px solid var(--hk-danger-soft)',
}
