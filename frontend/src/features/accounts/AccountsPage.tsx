import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../../lib/api'
import { StatusBadge, healthTone, type BadgeTone } from '../../ui/StatusBadge'
import { listProviderAccounts } from './api'
import { AccountBulkByTag } from './AccountBulkByTag'
import { CreateAccountModal } from './CreateAccountModal'
import { HealthSummaryCard } from './HealthSummaryCard'
import {
  ACCOUNT_STATE_OPTIONS,
  ACCOUNTS_PAGE_LIMIT,
  EMPTY_ACCOUNT_FILTERS,
  type AccountListFilters,
} from './query'
import type { ProviderAccount } from './types'

/*
 * 账号中心 · 列表页(P0)。
 * 接后端 GET /admin/v1/provider-accounts:多维筛选(状态/池组/标签)+ 游标分页 + 表格。
 * name 搜索为本页客户端过滤(后端无 name query 参数,故明确限定"本页内")。
 */

export function AccountsPage() {
  // 已提交的筛选(驱动请求);草稿在输入框,点"查询"才提交,避免每键一请求。
  const [filters, setFilters] = useState<AccountListFilters>(EMPTY_ACCOUNT_FILTERS)
  const [draftState, setDraftState] = useState<AccountListFilters['stateFilter']>('')
  const [draftPool, setDraftPool] = useState('')
  const [draftTag, setDraftTag] = useState('')
  const [nameQuery, setNameQuery] = useState('')

  // 游标栈:支持"上一页"。栈顶=当前页起始 cursor。
  const [cursorStack, setCursorStack] = useState<(string | null)[]>([null])

  const [items, setItems] = useState<ProviderAccount[]>([])
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  // 创建成功后自增以强制重拉列表。
  const [refreshNonce, setRefreshNonce] = useState(0)

  const currentCursor = cursorStack[cursorStack.length - 1]

  const load = useCallback(
    (f: AccountListFilters, signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listProviderAccounts(f, signal)
        .then((resp) => {
          setItems(resp.items)
          setNextCursor(resp.page.has_more ? resp.page.next_cursor : null)
        })
        .catch((e: unknown) => {
          if (signal.aborted) return
          const msg = e instanceof ApiError ? `${e.message}(${e.code})` : '加载账号列表失败'
          setError(msg)
          setItems([])
          setNextCursor(null)
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load({ ...filters, cursor: currentCursor }, ctrl.signal)
    return () => ctrl.abort()
  }, [filters, currentCursor, load, refreshNonce])

  const applyFilters = () => {
    setCursorStack([null])
    setFilters({
      ...EMPTY_ACCOUNT_FILTERS,
      stateFilter: draftState,
      poolGroupId: draftPool,
      tag: draftTag,
    })
  }

  const resetFilters = () => {
    setDraftState('')
    setDraftPool('')
    setDraftTag('')
    setNameQuery('')
    setCursorStack([null])
    setFilters(EMPTY_ACCOUNT_FILTERS)
  }

  const goNext = () => {
    if (nextCursor) setCursorStack((s) => [...s, nextCursor])
  }
  const goPrev = () => {
    setCursorStack((s) => (s.length > 1 ? s.slice(0, -1) : s))
  }

  const visible = useMemo(() => {
    const q = nameQuery.trim().toLowerCase()
    return q ? items.filter((a) => a.name.toLowerCase().includes(q)) : items
  }, [items, nameQuery])

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>账号中心</h1>
          <p className="hk-sub">
            管线第 1 站 · 上游账号池。共 {visible.length} 条
            {nameQuery.trim() && items.length !== visible.length ? `(本页内按名称过滤自 ${items.length})` : ''}。
          </p>
        </div>
        <button type="button" onClick={() => setCreateOpen(true)} className="hk-btn hk-btn--green">
          ＋ 新建账号
        </button>
      </header>

      <HealthSummaryCard />

      {createOpen && (
        <CreateAccountModal
          onClose={() => setCreateOpen(false)}
          onCreated={() => setRefreshNonce((n) => n + 1)}
        />
      )}

      <FilterBar
        draftState={draftState}
        draftPool={draftPool}
        draftTag={draftTag}
        nameQuery={nameQuery}
        onState={setDraftState}
        onPool={setDraftPool}
        onTag={setDraftTag}
        onName={setNameQuery}
        onApply={applyFilters}
        onReset={resetFilters}
      />

      {/* 按标签批量调参:批量启停/改优先级/改静态权重(POST /bulk-by-tag),应用后重拉列表。 */}
      <AccountBulkByTag onApplied={() => setRefreshNonce((n) => n + 1)} />

      <div className="hk-card">
        {error ? (
          <Notice tone="danger">{error}</Notice>
        ) : loading && items.length === 0 ? (
          <Notice tone="muted">加载中…</Notice>
        ) : visible.length === 0 ? (
          <Notice tone="muted">没有符合条件的账号。</Notice>
        ) : (
          <AccountsTable rows={visible} dim={loading} />
        )}
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
        <PagerButton disabled={cursorStack.length <= 1 || loading} onClick={goPrev}>
          上一页
        </PagerButton>
        <PagerButton disabled={!nextCursor || loading} onClick={goNext}>
          下一页
        </PagerButton>
        <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>
          第 {cursorStack.length} 页 · 每页 {ACCOUNTS_PAGE_LIMIT}
        </span>
      </div>
    </div>
  )
}

function FilterBar(props: {
  draftState: AccountListFilters['stateFilter']
  draftPool: string
  draftTag: string
  nameQuery: string
  onState: (v: AccountListFilters['stateFilter']) => void
  onPool: (v: string) => void
  onTag: (v: string) => void
  onName: (v: string) => void
  onApply: () => void
  onReset: () => void
}) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        props.onApply()
      }}
      className="hk-card"
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        gap: 'var(--hk-space-3)',
        alignItems: 'flex-end',
        padding: 'var(--hk-space-4)',
      }}
    >
      <Field label="状态">
        <select
          value={props.draftState}
          onChange={(e) => props.onState(e.target.value as AccountListFilters['stateFilter'])}
          style={selectStyle}
        >
          {ACCOUNT_STATE_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </Field>
      <Field label="池组 ID">
        <input
          value={props.draftPool}
          onChange={(e) => props.onPool(e.target.value)}
          inputMode="numeric"
          placeholder="如 12"
          style={inputStyle}
        />
      </Field>
      <Field label="标签">
        <input value={props.draftTag} onChange={(e) => props.onTag(e.target.value)} placeholder="如 prod" style={inputStyle} />
      </Field>
      <Field label="名称(本页过滤)">
        <input value={props.nameQuery} onChange={(e) => props.onName(e.target.value)} placeholder="按名称筛当前页" style={inputStyle} />
      </Field>
      <button type="submit" className="hk-btn hk-btn--green">
        查询
      </button>
      <button type="button" onClick={props.onReset} className="hk-btn">
        重置
      </button>
    </form>
  )
}

function AccountsTable({ rows, dim }: { rows: ProviderAccount[]; dim: boolean }) {
  return (
    <div className="hk-tablewrap" style={{ opacity: dim ? 0.6 : 1, transition: 'opacity .15s' }}>
      <table className="hk-table">
        <thead>
          <tr>
            {['名称', '类型', '启用', '健康', '凭据', '在途', '优先级', '权重', '并发上限', '最近派发'].map((h) => (
              <th key={h}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((a) => (
            <tr key={a.id}>
              <td>
                <div style={{ display: 'flex', flexDirection: 'column' }}>
                  <Link to={`/accounts/${a.id}`} style={{ fontWeight: 600, color: 'var(--hk-primary-700)' }}>
                    {a.name}
                  </Link>
                  {a.tags.length > 0 && (
                    <span style={{ color: 'var(--hk-ink-300)', fontSize: 11 }}>{a.tags.join(' · ')}</span>
                  )}
                </div>
              </td>
              <td>
                <span className="hk-mono">{a.account_type}</span>
              </td>
              <td>
                <StatusBadge tone={a.enabled ? 'ok' : 'muted'}>{a.enabled ? '已启用' : '已停用'}</StatusBadge>
              </td>
              <td>
                <StatusBadge tone={healthTone(a.health_state)}>{a.health_state || '—'}</StatusBadge>
              </td>
              <td>
                <StatusBadge tone={credentialTone(a.credential_state)}>{a.credential_state || '—'}</StatusBadge>
              </td>
              <td className="hk-mono" style={{ textAlign: 'right' }}>{a.in_flight_count}</td>
              <td className="hk-mono" style={{ textAlign: 'right' }}>{a.priority}</td>
              <td className="hk-mono" style={{ textAlign: 'right' }}>{a.static_weight}</td>
              <td className="hk-mono" style={{ textAlign: 'right' }}>{a.cap_concurrency}</td>
              <td className="hk-mono">{formatTime(a.last_dispatch_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function credentialTone(state: string): BadgeTone {
  switch (state) {
    case 'active':
    case 'valid':
      return 'ok'
    case 'expiring':
    case 'needs_rotation':
      return 'warn'
    case 'revoked':
    case 'expired':
    case 'invalid':
      return 'danger'
    default:
      return 'muted'
  }
}

function formatTime(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString('zh-CN', { hour12: false })
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}

function Notice({ tone, children }: { tone: 'danger' | 'muted'; children: React.ReactNode }) {
  return (
    <div
      style={{
        padding: 'var(--hk-space-6)',
        textAlign: 'center',
        color: tone === 'danger' ? 'var(--hk-danger)' : 'var(--hk-ink-500)',
        fontSize: 13,
      }}
    >
      {children}
    </div>
  )
}

function PagerButton({ disabled, onClick, children }: { disabled: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" disabled={disabled} onClick={onClick} className="hk-btn" style={{ opacity: disabled ? 0.5 : 1, cursor: disabled ? 'not-allowed' : 'pointer' }}>
      {children}
    </button>
  )
}

const inputStyle: React.CSSProperties = {
  height: 32,
  minWidth: 140,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-sm)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
}
const selectStyle: React.CSSProperties = { ...inputStyle }
