import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { listBillingClaims, listUsageRecords } from './api'
import {
  claimStatusTone,
  mapClaimTableRows,
  mapUsageTableRows,
  trustStatusTone,
  type ClaimTableRow,
  type UsageTableRow,
} from './billingadmin'
import { RepriceCard } from './RepriceCard'
import {
  EMPTY_CLAIM_FILTERS,
  EMPTY_USAGE_FILTERS,
  type BillingClaim,
  type ClaimFilters,
  type UsageFilters,
  type UsageRecord,
} from './types'

/*
 * 计费运营 · 用量与 claim 台账(运营台,stage5)。两端点纯只读 money 观测:
 *   - GET /admin/v1/usage          原始逐笔用量成本表
 *   - GET /admin/v1/billing/claims 预扣/结算 claim 台账
 * 两区独立:各自过滤 + 游标分页(加载更多)。money 数额十进制字符串原样渲染,无任何写动作。
 */
type Tab = 'usage' | 'claims'

export function BillingClaimsPage() {
  const [tab, setTab] = useState<Tab>('usage')
  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>用量与计费台账</h1>
          <p className="hk-sub">
            运营台 · 计费运营。查看逐笔成本与 claim 台账，并对待对账记录按当前价表执行受控重算。
          </p>
        </div>
      </header>

      <RepriceCard />

      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', borderBottom: '1px solid var(--hk-line)' }}>
        <TabBtn active={tab === 'usage'} onClick={() => setTab('usage')}>
          原始用量
        </TabBtn>
        <TabBtn active={tab === 'claims'} onClick={() => setTab('claims')}>
          Claim 台账
        </TabBtn>
      </div>

      {tab === 'usage' ? <UsageSection /> : <ClaimsSection />}
    </div>
  )
}

// ── 原始用量区 ──────────────────────────────────────────────────────────────

function UsageSection() {
  const [rows, setRows] = useState<UsageRecord[]>([])
  const [total, setTotal] = useState(0)
  const [nextCursor, setNextCursor] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<UsageFilters>(EMPTY_USAGE_FILTERS)
  const [filters, setFilters] = useState<UsageFilters>(EMPTY_USAGE_FILTERS)
  const [expanded, setExpanded] = useState<number | null>(null)

  const loadFirst = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listUsageRecords(filters, undefined, 100, signal)
        .then((resp) => {
          setRows(resp.items)
          setTotal(resp.total)
          setNextCursor(resp.next_cursor)
        })
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载用量记录失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [filters],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    loadFirst(ctrl.signal)
    return () => ctrl.abort()
  }, [loadFirst])

  const loadMore = async () => {
    if (!nextCursor) return
    setLoading(true)
    setError(null)
    try {
      const resp = await listUsageRecords(filters, nextCursor, 100)
      setRows((prev) => [...prev, ...resp.items])
      setNextCursor(resp.next_cursor)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载更多失败')
    } finally {
      setLoading(false)
    }
  }

  const setD = <K extends keyof UsageFilters>(k: K, v: UsageFilters[K]) => setDraft((f) => ({ ...f, [k]: v }))
  const tableRows = mapUsageTableRows(rows)
  const expandedRow = tableRows.find((row) => row.id === expanded)

  return (
    <>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          setExpanded(null)
          setFilters(draft)
        }}
        style={filterFormStyle}
      >
        <Field label="供应商(provider)">
          <input value={draft.provider} onChange={(e) => setD('provider', e.target.value)} placeholder="如 anthropic" style={inp} />
        </Field>
        <Field label="模型(model)">
          <input value={draft.model} onChange={(e) => setD('model', e.target.value)} style={inp} />
        </Field>
        <Field label="池 ID">
          <input value={draft.poolId} onChange={(e) => setD('poolId', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="API Key ID">
          <input value={draft.apiKeyId} onChange={(e) => setD('apiKeyId', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="供应商账号 ID">
          <input value={draft.providerAccountId} onChange={(e) => setD('providerAccountId', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="结局(outcome)">
          <select value={draft.outcome} onChange={(e) => setD('outcome', e.target.value)} style={inp}>
            <option value="">全部</option>
            <option value="success">成功</option>
            <option value="error">失败</option>
          </select>
        </Field>
        <Field label="起(本地时间)">
          <input type="datetime-local" value={draft.from} onChange={(e) => setD('from', e.target.value)} style={inp} />
        </Field>
        <Field label="止(本地时间)">
          <input type="datetime-local" value={draft.to} onChange={(e) => setD('to', e.target.value)} style={inp} />
        </Field>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--hk-ink-500)' }}>
          <input type="checkbox" checked={draft.pendingOnly} onChange={(e) => setD('pendingOnly', e.target.checked)} />
          仅待对账
        </label>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
          <button type="submit" className="hk-btn hk-btn--green">
            查询
          </button>
          <button type="button" onClick={() => { setDraft(EMPTY_USAGE_FILTERS); setFilters(EMPTY_USAGE_FILTERS) }} className="hk-btn">
            重置
          </button>
        </div>
      </form>

      <Counter loaded={rows.length} total={total} unit="条用量记录" />
      {error && <ErrorBar>{error}</ErrorBar>}

      <div className="hk-card">
        {loading && rows.length === 0 ? (
          <EmptyState title="正在加载用量记录" hint="请稍候。" />
        ) : rows.length === 0 ? (
          <EmptyState title="没有匹配的用量记录" hint="可调整筛选条件后重新查询。" />
        ) : (
          <>
            <DataListTable
              label="原始用量列表"
              rows={tableRows}
              rowKey={(row) => row.id}
              columns={usageColumns}
              actions={[{
                label: (row) => expanded === row.id ? '收起' : '详情',
                onClick: (row) => setExpanded((current) => current === row.id ? null : row.id),
              }]}
            />
            {expandedRow && <pre style={detailPreStyle}>{JSON.stringify(expandedRow.source, null, 2)}</pre>}
          </>
        )}
      </div>

      {nextCursor && (
        <button type="button" disabled={loading} onClick={loadMore} className="hk-btn" style={{ alignSelf: 'center' }}>
          {loading ? '加载中…' : '加载更多'}
        </button>
      )}
    </>
  )
}

// ── Claim 台账区 ────────────────────────────────────────────────────────────

function ClaimsSection() {
  const [rows, setRows] = useState<BillingClaim[]>([])
  const [total, setTotal] = useState(0)
  const [nextCursor, setNextCursor] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<ClaimFilters>(EMPTY_CLAIM_FILTERS)
  const [filters, setFilters] = useState<ClaimFilters>(EMPTY_CLAIM_FILTERS)
  const [expanded, setExpanded] = useState<number | null>(null)

  const loadFirst = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listBillingClaims(filters, undefined, 100, signal)
        .then((resp) => {
          setRows(resp.items)
          setTotal(resp.total)
          setNextCursor(resp.next_cursor)
        })
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载 claim 台账失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [filters],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    loadFirst(ctrl.signal)
    return () => ctrl.abort()
  }, [loadFirst])

  const loadMore = async () => {
    if (!nextCursor) return
    setLoading(true)
    setError(null)
    try {
      const resp = await listBillingClaims(filters, nextCursor, 100)
      setRows((prev) => [...prev, ...resp.items])
      setNextCursor(resp.next_cursor)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载更多失败')
    } finally {
      setLoading(false)
    }
  }

  const setD = <K extends keyof ClaimFilters>(k: K, v: ClaimFilters[K]) => setDraft((f) => ({ ...f, [k]: v }))
  const tableRows = mapClaimTableRows(rows)
  const expandedRow = tableRows.find((row) => row.id === expanded)

  return (
    <>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          setExpanded(null)
          setFilters(draft)
        }}
        style={filterFormStyle}
      >
        <Field label="状态(status)">
          <input value={draft.status} onChange={(e) => setD('status', e.target.value)} placeholder="如 committed" style={inp} />
        </Field>
        <Field label="供应商(provider)">
          <input value={draft.provider} onChange={(e) => setD('provider', e.target.value)} style={inp} />
        </Field>
        <Field label="模型(model)">
          <input value={draft.model} onChange={(e) => setD('model', e.target.value)} style={inp} />
        </Field>
        <Field label="池 ID">
          <input value={draft.poolId} onChange={(e) => setD('poolId', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="API Key ID">
          <input value={draft.apiKeyId} onChange={(e) => setD('apiKeyId', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="供应商账号 ID">
          <input value={draft.providerAccountId} onChange={(e) => setD('providerAccountId', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="起(本地时间)">
          <input type="datetime-local" value={draft.from} onChange={(e) => setD('from', e.target.value)} style={inp} />
        </Field>
        <Field label="止(本地时间)">
          <input type="datetime-local" value={draft.to} onChange={(e) => setD('to', e.target.value)} style={inp} />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
          <button type="submit" className="hk-btn hk-btn--green">
            查询
          </button>
          <button type="button" onClick={() => { setDraft(EMPTY_CLAIM_FILTERS); setFilters(EMPTY_CLAIM_FILTERS) }} className="hk-btn">
            重置
          </button>
        </div>
      </form>

      <Counter loaded={rows.length} total={total} unit="条 claim" />
      {error && <ErrorBar>{error}</ErrorBar>}

      <div className="hk-card">
        {loading && rows.length === 0 ? (
          <EmptyState title="正在加载 Claim 台账" hint="请稍候。" />
        ) : rows.length === 0 ? (
          <EmptyState title="没有匹配的 Claim" hint="可调整筛选条件后重新查询。" />
        ) : (
          <>
            <DataListTable
              label="Claim 台账列表"
              rows={tableRows}
              rowKey={(row) => row.id}
              columns={claimColumns}
              actions={[{
                label: (row) => expanded === row.id ? '收起' : '详情',
                onClick: (row) => setExpanded((current) => current === row.id ? null : row.id),
              }]}
            />
            {expandedRow && <pre style={detailPreStyle}>{JSON.stringify(expandedRow.source, null, 2)}</pre>}
          </>
        )}
      </div>

      {nextCursor && (
        <button type="button" disabled={loading} onClick={loadMore} className="hk-btn" style={{ alignSelf: 'center' }}>
          {loading ? '加载中…' : '加载更多'}
        </button>
      )}
    </>
  )
}

// ── 共用小组件与样式 ─────────────────────────────────────────────────────────

function TabBtn({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        border: 'none',
        background: 'transparent',
        padding: 'var(--hk-space-2) var(--hk-space-3)',
        fontSize: 14,
        fontWeight: active ? 600 : 400,
        color: active ? 'var(--hk-primary-700)' : 'var(--hk-ink-500)',
        borderBottom: active ? '2px solid var(--hk-primary-500)' : '2px solid transparent',
        marginBottom: -1,
        cursor: 'pointer',
      }}
    >
      {children}
    </button>
  )
}

function Counter({ loaded, total, unit }: { loaded: number; total: number; unit: string }) {
  return (
    <p style={{ margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      已载 {loaded} / 共 {total} {unit}。
    </p>
  )
}

function ErrorBar({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{children}</div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}

const filterFormStyle: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))',
  gap: 'var(--hk-space-3)',
  alignItems: 'flex-end',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  padding: 'var(--hk-space-4)',
}
const detailPreStyle: React.CSSProperties = { margin: 0, padding: 'var(--hk-space-4)', borderTop: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)', fontFamily: 'var(--hk-font-mono)', fontSize: 12, color: 'var(--hk-ink-700)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }

const usageColumns: DataListColumn<UsageTableRow>[] = [
  { key: 'createdAt', label: '时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
  { key: 'model', label: '模型', render: (row) => <div style={{ display: 'flex', flexDirection: 'column' }}><strong>{row.requestedModel}</strong>{row.upstreamModel && <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>→ {row.upstreamModel}</span>}</div> },
  { key: 'provider', label: '供应商', render: (row) => row.provider },
  { key: 'tokens', label: 'Token(入/出)', render: (row) => <span className="hk-mono">{row.tokens}</span> },
  { key: 'actualCost', label: '实际成本', render: (row) => <strong className="hk-mono">{row.actualCost}</strong> },
  { key: 'trustStatus', label: '信任态', render: (row) => <div><StatusBadge tone={trustStatusTone(row.trustStatus)}>{row.trustStatus}</StatusBadge>{row.pendingReconciliation && <div style={{ marginTop: 2 }}><StatusBadge tone="warn">待对账</StatusBadge></div>}</div> },
  { key: 'requestId', label: 'Request', render: (row) => <span className="hk-mono">{row.requestId}</span> },
]

const claimColumns: DataListColumn<ClaimTableRow>[] = [
  { key: 'createdAt', label: '时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
  { key: 'model', label: '模型/端点', render: (row) => <div style={{ display: 'flex', flexDirection: 'column' }}><strong>{row.requestedModel}</strong><span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{row.endpointFamily}</span></div> },
  { key: 'status', label: '状态', render: (row) => <div><StatusBadge tone={claimStatusTone(row.status)}>{row.status}</StatusBadge>{row.abortedReason && <div style={{ fontSize: 11, color: 'var(--hk-ink-300)', marginTop: 2 }}>{row.abortedReason}</div>}</div> },
  { key: 'predictedCost', label: '预扣成本', render: (row) => <span className="hk-mono">{row.predictedCost}</span> },
  { key: 'actualCost', label: '实际成本', render: (row) => <strong className="hk-mono">{row.actualCost}</strong> },
  { key: 'settledAt', label: '结算时间', render: (row) => <span className="hk-mono">{row.settledAt}</span> },
  { key: 'requestId', label: 'Request', render: (row) => <span className="hk-mono">{row.requestId}</span> },
]
