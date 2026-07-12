import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listBillingClaims, listUsageRecords } from './api'
import { claimStatusTone, formatMoney, formatTime, shortId, trustStatusTone } from './billingadmin'
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
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22, margin: 0 }}>用量与计费台账</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          运营台 · 计费运营。原始逐笔用量成本与预扣/结算 claim 台账(只读)。金额按十进制原样展示,不做四舍五入。
        </p>
      </header>

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
          <button type="submit" style={primaryBtn}>
            查询
          </button>
          <button type="button" onClick={() => { setDraft(EMPTY_USAGE_FILTERS); setFilters(EMPTY_USAGE_FILTERS) }} style={ghostBtn}>
            重置
          </button>
        </div>
      </form>

      <Counter loaded={rows.length} total={total} unit="条用量记录" />
      {error && <ErrorBar>{error}</ErrorBar>}

      <TableShell empty={!loading && rows.length === 0} loading={loading && rows.length === 0} emptyText="没有匹配的用量记录。">
        <table style={tableStyle}>
          <thead>
            <tr>
              {['时间', '模型', '供应商', 'Token(入/出)', '实际成本', '信任态', 'Request', ''].map((h) => (
                <th key={h} style={th}>
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <UsageRow key={r.id} r={r} expanded={expanded === r.id} onToggle={() => setExpanded(expanded === r.id ? null : r.id)} />
            ))}
          </tbody>
        </table>
      </TableShell>

      {nextCursor && (
        <button type="button" disabled={loading} onClick={loadMore} style={{ ...ghostBtn, alignSelf: 'center', height: 36 }}>
          {loading ? '加载中…' : '加载更多'}
        </button>
      )}
    </>
  )
}

function UsageRow({ r, expanded, onToggle }: { r: UsageRecord; expanded: boolean; onToggle: () => void }) {
  return (
    <>
      <tr style={{ borderTop: '1px solid var(--hk-line)' }}>
        <td style={tdMono}>{formatTime(r.created_at)}</td>
        <td style={td}>
          <div style={{ display: 'flex', flexDirection: 'column' }}>
            <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{r.requested_model || '—'}</span>
            {r.upstream_model && r.upstream_model !== r.requested_model && (
              <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>→ {r.upstream_model}</span>
            )}
          </div>
        </td>
        <td style={td}>{r.provider || '—'}</td>
        <td style={tdMono}>
          {r.tokens_input} / {r.tokens_output}
        </td>
        <td style={{ ...tdMono, fontWeight: 600 }}>{formatMoney(r.actual_cost)}</td>
        <td style={td}>
          <StatusBadge tone={trustStatusTone(r.trust_status)}>{r.trust_status || '—'}</StatusBadge>
          {r.pending_reconciliation && (
            <div style={{ marginTop: 2 }}>
              <StatusBadge tone="warn">待对账</StatusBadge>
            </div>
          )}
        </td>
        <td style={tdMono}>{shortId(r.request_id)}</td>
        <td style={{ ...td, textAlign: 'right' }}>
          <button type="button" onClick={onToggle} style={linkBtn}>
            {expanded ? '收起' : '详情'}
          </button>
        </td>
      </tr>
      {expanded && (
        <tr style={{ background: 'var(--hk-surface-sunken)' }}>
          <td colSpan={8} style={{ padding: 'var(--hk-space-4)' }}>
            <pre style={preStyle}>{JSON.stringify(r, null, 2)}</pre>
          </td>
        </tr>
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
          <button type="submit" style={primaryBtn}>
            查询
          </button>
          <button type="button" onClick={() => { setDraft(EMPTY_CLAIM_FILTERS); setFilters(EMPTY_CLAIM_FILTERS) }} style={ghostBtn}>
            重置
          </button>
        </div>
      </form>

      <Counter loaded={rows.length} total={total} unit="条 claim" />
      {error && <ErrorBar>{error}</ErrorBar>}

      <TableShell empty={!loading && rows.length === 0} loading={loading && rows.length === 0} emptyText="没有匹配的 claim。">
        <table style={tableStyle}>
          <thead>
            <tr>
              {['时间', '模型/端点', '状态', '预扣成本', '实际成本', '结算时间', 'Request', ''].map((h) => (
                <th key={h} style={th}>
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((c) => (
              <ClaimRow key={c.id} c={c} expanded={expanded === c.id} onToggle={() => setExpanded(expanded === c.id ? null : c.id)} />
            ))}
          </tbody>
        </table>
      </TableShell>

      {nextCursor && (
        <button type="button" disabled={loading} onClick={loadMore} style={{ ...ghostBtn, alignSelf: 'center', height: 36 }}>
          {loading ? '加载中…' : '加载更多'}
        </button>
      )}
    </>
  )
}

function ClaimRow({ c, expanded, onToggle }: { c: BillingClaim; expanded: boolean; onToggle: () => void }) {
  return (
    <>
      <tr style={{ borderTop: '1px solid var(--hk-line)' }}>
        <td style={tdMono}>{formatTime(c.created_at)}</td>
        <td style={td}>
          <div style={{ display: 'flex', flexDirection: 'column' }}>
            <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{c.requested_model || '—'}</span>
            <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{c.endpoint_family || '—'}</span>
          </div>
        </td>
        <td style={td}>
          <StatusBadge tone={claimStatusTone(c.status)}>{c.status || '—'}</StatusBadge>
          {c.aborted_reason && (
            <div style={{ fontSize: 11, color: 'var(--hk-ink-300)', marginTop: 2 }}>{c.aborted_reason}</div>
          )}
        </td>
        <td style={tdMono}>{formatMoney(c.predicted_cost, c.currency_code)}</td>
        <td style={{ ...tdMono, fontWeight: 600 }}>{formatMoney(c.actual_cost, c.currency_code)}</td>
        <td style={tdMono}>{formatTime(c.settled_at)}</td>
        <td style={tdMono}>{shortId(c.logical_request_id)}</td>
        <td style={{ ...td, textAlign: 'right' }}>
          <button type="button" onClick={onToggle} style={linkBtn}>
            {expanded ? '收起' : '详情'}
          </button>
        </td>
      </tr>
      {expanded && (
        <tr style={{ background: 'var(--hk-surface-sunken)' }}>
          <td colSpan={8} style={{ padding: 'var(--hk-space-4)' }}>
            <pre style={preStyle}>{JSON.stringify(c, null, 2)}</pre>
          </td>
        </tr>
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

function TableShell({
  empty,
  loading,
  emptyText,
  children,
}: {
  empty: boolean
  loading: boolean
  emptyText: string
  children: React.ReactNode
}) {
  return (
    <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }}>
      {loading ? <Empty>加载中…</Empty> : empty ? <Empty>{emptyText}</Empty> : <div style={{ overflowX: 'auto' }}>{children}</div>}
    </div>
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

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
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
const tableStyle: React.CSSProperties = { width: '100%', borderCollapse: 'collapse', fontSize: 13 }
const preStyle: React.CSSProperties = { margin: 0, fontFamily: 'var(--hk-font-mono)', fontSize: 12, color: 'var(--hk-ink-700)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'top' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
