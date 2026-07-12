import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { formatCost, modelDisplay, statusLabel, statusTone, tokensSummary } from '../usagerecords/usagerecords'
import { getKeyGeneration, getKeyUsageTimeSeries, listKeyUsageRecords } from './api'
import {
  aggregateTokenCount,
  buildKeyAnalyticsWindow,
  costBarPercent,
  defaultKeyAnalyticsRange,
} from './keyAnalytics'
import type {
  KeyUsageGranularity,
  KeyUsageRecord,
  KeyUsageTimeSeriesPoint,
  KeyUsageTimeSeriesResponse,
} from './types'

const PAGE_LIMIT = 50

interface ActiveQuery {
  apiKey: string
  from: string
  to: string
}

export function KeyUsageAnalytics() {
  const initial = defaultKeyAnalyticsRange()
  const [apiKey, setApiKey] = useState('')
  const [fromDay, setFromDay] = useState(initial.fromDay)
  const [toDay, setToDay] = useState(initial.toDay)
  const [granularity, setGranularity] = useState<KeyUsageGranularity>('day')
  const [series, setSeries] = useState<KeyUsageTimeSeriesResponse | null>(null)
  const [records, setRecords] = useState<KeyUsageRecord[]>([])
  const [cursor, setCursor] = useState('')
  const [active, setActive] = useState<ActiveQuery | null>(null)
  const [loading, setLoading] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [requestId, setRequestId] = useState('')
  const [detail, setDetail] = useState<KeyUsageRecord | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<string | null>(null)

  const resetResults = () => {
    setSeries(null)
    setRecords([])
    setCursor('')
    setActive(null)
    setDetail(null)
    setDetailError(null)
  }

  const changeApiKey = (value: string) => {
    if (active && value.trim() !== active.apiKey) resetResults()
    setApiKey(value)
    setError(null)
  }

  const changeRange = (side: 'from' | 'to', value: string) => {
    if (active) resetResults()
    if (side === 'from') setFromDay(value)
    else setToDay(value)
    setError(null)
  }

  const changeGranularity = (value: KeyUsageGranularity) => {
    if (active) resetResults()
    setGranularity(value)
    setError(null)
  }

  const runAnalysis = async () => {
    const key = apiKey.trim()
    if (!key) {
      setError('请先粘贴要分析的 API Key')
      return
    }
    const window = buildKeyAnalyticsWindow(fromDay, toDay)
    if (!window.ok) {
      setError(window.error)
      return
    }
    setLoading(true)
    setError(null)
    setDetail(null)
    setDetailError(null)
    try {
      const [timeline, page] = await Promise.all([
        getKeyUsageTimeSeries(key, { ...window.value, granularity }),
        listKeyUsageRecords(key, { ...window.value, limit: PAGE_LIMIT }),
      ])
      setSeries(timeline)
      setRecords(page.items)
      setCursor(page.next_cursor)
      setActive({ apiKey: key, ...window.value })
    } catch (cause) {
      resetResults()
      setError(errorText(cause, '加载 Key 级分析失败'))
    } finally {
      setLoading(false)
    }
  }

  const loadMore = async () => {
    if (!active || !cursor || loadingMore) return
    setLoadingMore(true)
    setError(null)
    try {
      const page = await listKeyUsageRecords(active.apiKey, {
        limit: PAGE_LIMIT,
        cursor,
        from: active.from,
        to: active.to,
      })
      setRecords((current) => [...current, ...page.items])
      setCursor(page.next_cursor)
    } catch (cause) {
      setError(errorText(cause, '加载更多 Key 请求失败'))
    } finally {
      setLoadingMore(false)
    }
  }

  const queryGeneration = async () => {
    const key = apiKey.trim()
    const id = requestId.trim()
    if (!key) {
      setDetailError('请先粘贴 API Key')
      return
    }
    if (!id) {
      setDetailError('请输入 request_id')
      return
    }
    setDetailLoading(true)
    setDetail(null)
    setDetailError(null)
    try {
      setDetail(await getKeyGeneration(key, id))
    } catch (cause) {
      setDetailError(errorText(cause, '未找到该请求或查询失败'))
    } finally {
      setDetailLoading(false)
    }
  }

  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>Key 级分析</h3>
        <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>API Key 仅保留在本页内存，不保存</span>
      </div>
      <div className="hk-card__body" style={column}>
        <div style={toolbar}>
          <label style={{ ...field, minWidth: 240, flex: 1 }}>
            <span style={label}>当前 API Key</span>
            <input
              type="password"
              value={apiKey}
              onChange={(event) => changeApiKey(event.target.value)}
              placeholder="hk_..."
              autoComplete="off"
              spellCheck={false}
              style={input}
              aria-label="Key 级分析 API Key"
            />
          </label>
          <label style={field}>
            <span style={label}>开始日期（UTC）</span>
            <input type="date" value={fromDay} max={toDay} onChange={(event) => changeRange('from', event.target.value)} style={input} />
          </label>
          <label style={field}>
            <span style={label}>结束日期（UTC）</span>
            <input type="date" value={toDay} min={fromDay} onChange={(event) => changeRange('to', event.target.value)} style={input} />
          </label>
          <div style={field}>
            <span style={label}>粒度</span>
            <div className="hk-seg">
              {(['day', 'week', 'month'] as const).map((value) => (
                <button key={value} type="button" className={granularity === value ? 'is-on' : ''} onClick={() => changeGranularity(value)}>
                  {{ day: '日', week: '周', month: '月' }[value]}
                </button>
              ))}
            </div>
          </div>
          <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignSelf: 'end' }}>
            <button type="button" onClick={() => void runAnalysis()} disabled={loading} className="hk-btn hk-btn--green">
              {loading ? '查询中…' : '查询此 Key'}
            </button>
            <button type="button" onClick={() => { setApiKey(''); resetResults(); setError(null) }} disabled={loading} className="hk-btn">清除</button>
          </div>
        </div>

        {error && <Notice>{error}</Notice>}

        <div style={grid}>
          <KeyUsageChart response={series} loading={loading} />
          <RequestLookup
            requestId={requestId}
            detail={detail}
            loading={detailLoading}
            error={detailError}
            onRequestIdChange={(value) => { setRequestId(value); setDetail(null); setDetailError(null) }}
            onQuery={() => void queryGeneration()}
          />
        </div>

        <KeyUsageTable
          records={records}
          analyzed={active !== null}
          loading={loading}
          nextCursor={cursor}
          loadingMore={loadingMore}
          onLoadMore={() => void loadMore()}
        />
      </div>
    </section>
  )
}

export function KeyUsageChart({ response, loading }: { response: KeyUsageTimeSeriesResponse | null; loading: boolean }) {
  return (
    <section style={subpanel}>
      <div style={subpanelHead}>
        <h4 style={subpanelTitle}>费用与 Token 时间序列</h4>
        {response && <span style={subtle}>{formatPeriod(response.period.from, response.period.to)}</span>}
      </div>
      {loading && !response ? (
        <div className="hk-empty">加载时间序列…</div>
      ) : !response ? (
        <div className="hk-empty">输入 API Key 后查询最近 30 天。</div>
      ) : response.items.length === 0 ? (
        <div className="hk-empty">所选时段暂无用量。</div>
      ) : (
        <div style={column}>
          {response.items.map((point, index) => (
            <TimeSeriesRow key={`${point.day}-${point.requested_model}-${index}`} point={point} points={response.items} />
          ))}
        </div>
      )}
    </section>
  )
}

function TimeSeriesRow({ point, points }: { point: KeyUsageTimeSeriesPoint; points: KeyUsageTimeSeriesPoint[] }) {
  const percent = costBarPercent(point, points)
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '86px minmax(100px, 1fr) minmax(80px, 1.4fr) 88px', gap: 'var(--hk-space-2)', alignItems: 'center', fontSize: 12 }}>
      <span className="hk-mono" style={{ color: 'var(--hk-ink-500)' }}>{point.day || '—'}</span>
      <span title={point.requested_model} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{point.requested_model || '未标模型'}</span>
      <div>
        <div className="hk-bar"><span style={{ width: `${percent}%` }} /></div>
        <span style={{ ...subtle, fontSize: 10 }}>{point.request_count} 请求 · {aggregateTokenCount(point)} Token</span>
      </div>
      <code style={{ textAlign: 'right', fontSize: 11 }}>{formatCost(point.total_cost)}</code>
    </div>
  )
}

interface TableProps {
  records: KeyUsageRecord[]
  analyzed: boolean
  loading: boolean
  nextCursor: string
  loadingMore: boolean
  onLoadMore: () => void
}

export function KeyUsageTable(props: TableProps) {
  return (
    <section style={subpanel}>
      <div style={subpanelHead}>
        <h4 style={subpanelTitle}>逐笔请求（当前 Key）</h4>
        <span style={subtle}>已加载 {props.records.length} 条</span>
      </div>
      {props.loading && props.records.length === 0 ? (
        <div className="hk-empty">加载请求记录…</div>
      ) : !props.analyzed ? (
        <div className="hk-empty">查询 Key 后显示逐笔请求。</div>
      ) : props.records.length === 0 ? (
        <div className="hk-empty">所选时段暂无请求。</div>
      ) : (
        <div className="hk-tablewrap">
          <table className="hk-table">
            <thead><tr>{['时间', '模型', '状态', '费用', 'Token', 'Provider', '请求 ID'].map((title) => <th key={title}>{title}</th>)}</tr></thead>
            <tbody>
              {props.records.map((record, index) => (
                <tr key={record.request_id || record.ledger_id || `${record.created_at}-${index}`}>
                  <td className="hk-mono">{formatTime(record.created_at)}</td>
                  <td>{modelDisplay(record)}</td>
                  <td><StatusBadge tone={statusTone(record.status) as BadgeTone}>{statusLabel(record.status)}</StatusBadge></td>
                  <td className="hk-mono">{formatCost(record.actual_cost)}</td>
                  <td>{tokensSummary(record.tokens)}</td>
                  <td>{record.provider || '—'}</td>
                  <td><code style={{ fontSize: 11 }}>{record.request_id || '—'}</code></td>
                </tr>
              ))}
            </tbody>
          </table>
          {props.nextCursor && (
            <button type="button" className="hk-loadmore" style={loadMoreButton} disabled={props.loadingMore || props.loading} onClick={props.onLoadMore}>
              {props.loadingMore ? '加载中…' : '加载更多'}
            </button>
          )}
        </div>
      )}
    </section>
  )
}

interface RequestLookupProps {
  requestId: string
  detail: KeyUsageRecord | null
  loading: boolean
  error: string | null
  onRequestIdChange: (value: string) => void
  onQuery: () => void
}

function RequestLookup(props: RequestLookupProps) {
  return (
    <section style={subpanel}>
      <h4 style={subpanelTitle}>按请求 ID 查询单笔</h4>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
        <input value={props.requestId} onChange={(event) => props.onRequestIdChange(event.target.value)} placeholder="request_id" style={{ ...input, flex: 1 }} aria-label="请求 ID" />
        <button type="button" onClick={props.onQuery} disabled={props.loading} className="hk-btn">
          {props.loading ? '查询中…' : '查询'}
        </button>
      </div>
      {props.error && <Notice>{props.error}</Notice>}
      {props.detail ? <KeyGenerationDetail record={props.detail} /> : <div className="hk-empty">详情严格限定到当前 API Key。</div>}
    </section>
  )
}

export function KeyGenerationDetail({ record }: { record: KeyUsageRecord }) {
  const rows = [
    ['请求 ID', record.request_id || '—'],
    ['模型', modelDisplay(record)],
    ['Provider', record.provider || '—'],
    ['状态', statusLabel(record.status)],
    ['实际费用', formatCost(record.actual_cost)],
    ['Token', tokensSummary(record.tokens)],
    ['请求时间', formatTime(record.requested_at || record.created_at)],
    ['流式', record.stream ? `是${record.stream_terminated_reason ? ` · ${record.stream_terminated_reason}` : ''}` : '否'],
    ['Ledger ID', record.ledger_id || '—'],
  ]
  return (
    <div className="hk-kv" style={{ marginTop: 'var(--hk-space-2)' }}>
      {rows.map(([key, value]) => (
        <div className="hk-kv__r" key={key}>
          <span className="hk-kv__k">{key}</span>
          <span className="hk-kv__v hk-mono" style={{ wordBreak: 'break-all' }}>{value}</span>
        </div>
      ))}
    </div>
  )
}

function Notice({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', borderRadius: 'var(--hk-radius-sm)', fontSize: 12 }}>{children}</div>
}

function errorText(cause: unknown, fallback: string): string {
  return cause instanceof ApiError ? `${cause.message}(${cause.code})` : fallback
}

function formatPeriod(from: string, to: string): string {
  return `${from.slice(0, 10)} 至 ${to.slice(0, 10)}（右界不含）`
}

function formatTime(value: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

const column: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }
const toolbar: React.CSSProperties = { display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)', alignItems: 'end', padding: 'var(--hk-space-3)', background: 'var(--hk-surface-sunken)', border: '1px solid var(--hk-line-soft)', borderRadius: 'var(--hk-radius-md)' }
const field: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }
const label: React.CSSProperties = { fontSize: 11.5, color: 'var(--hk-ink-500)' }
const input: React.CSSProperties = { minWidth: 0, padding: '7px 9px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontFamily: 'inherit' }
const grid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: 'var(--hk-space-3)', alignItems: 'start' }
const subpanel: React.CSSProperties = { border: '1px solid var(--hk-line-soft)', borderRadius: 'var(--hk-radius-md)', padding: 'var(--hk-space-3)', background: 'var(--hk-surface)' }
const subpanelHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-2)', marginBottom: 'var(--hk-space-3)' }
const subpanelTitle: React.CSSProperties = { margin: 0, fontSize: 13, color: 'var(--hk-ink-900)' }
const subtle: React.CSSProperties = { color: 'var(--hk-ink-300)', fontSize: 11 }
const loadMoreButton: React.CSSProperties = { width: '100%', background: 'transparent', borderRight: 0, borderBottom: 0, borderLeft: 0, fontFamily: 'inherit' }
