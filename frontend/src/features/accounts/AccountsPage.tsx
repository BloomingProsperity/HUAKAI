import { useCallback, useEffect, useMemo, useState, type CSSProperties } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../../lib/api'
import { useMe } from '../../auth/me'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { SkeletonRows } from '../../ui/Skeleton'
import { StatusBadge } from '../../ui/StatusBadge'
import { filterAccountRows, mapAccountRows, type AccountTableRow } from './accounts'
import {
  clearAccountRateLimit,
  deleteProviderAccount,
  listProviderAccounts,
  setAccountEnabled,
  testProviderAccount,
} from './api'
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
  const tenantId = useMe().tenantId
  // 已提交的筛选(驱动请求);草稿在输入框,点"查询"才提交,避免每键一请求。
  const [filters, setFilters] = useState<AccountListFilters>(EMPTY_ACCOUNT_FILTERS)
  const [draftState, setDraftState] = useState<AccountListFilters['stateFilter']>('')
  const [draftPool, setDraftPool] = useState('')
  const [draftTag, setDraftTag] = useState('')
  const [nameQuery, setNameQuery] = useState('')
  const [healthStateFilter, setHealthStateFilter] = useState('')

  // 游标栈:支持"上一页"。栈顶=当前页起始 cursor。
  const [cursorStack, setCursorStack] = useState<(string | null)[]>([null])

  const [items, setItems] = useState<ProviderAccount[]>([])
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [busyId, setBusyId] = useState<number | null>(null)
  // 创建成功后自增以强制重拉列表。
  const [refreshNonce, setRefreshNonce] = useState(0)

  const currentCursor = cursorStack[cursorStack.length - 1]

  const load = useCallback(
    (f: AccountListFilters, signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      if (tenantId == null) return
      listProviderAccounts(tenantId, f, signal)
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
    [tenantId],
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
    setHealthStateFilter('')
    setCursorStack([null])
    setFilters(EMPTY_ACCOUNT_FILTERS)
  }

  const goNext = () => {
    if (nextCursor) setCursorStack((s) => [...s, nextCursor])
  }
  const goPrev = () => {
    setCursorStack((s) => (s.length > 1 ? s.slice(0, -1) : s))
  }

  const rows = useMemo(() => mapAccountRows(items), [items])
  const visible = useMemo(
    () => filterAccountRows(rows, nameQuery, healthStateFilter),
    [healthStateFilter, nameQuery, rows],
  )
  const hasFilters = Boolean(
    filters.stateFilter || filters.poolGroupId || filters.tag || nameQuery.trim() || healthStateFilter,
  )

  const refresh = () => setRefreshNonce((nonce) => nonce + 1)

  const runAction = async (id: number, fallback: string, task: () => Promise<string>) => {
    if (busyId !== null) return
    setBusyId(id)
    setError(null)
    setFlash(null)
    try {
      setFlash(await task())
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : e instanceof Error ? e.message : fallback)
    } finally {
      setBusyId(null)
    }
  }

  const removeAccount = (account: ProviderAccount) => {
    if (!window.confirm(`确认删除账号「${account.name}」(#${account.id})？删除后账号会从可调度池和列表中移除。`)) return
    void runAction(account.id, '删除账号失败', async () => {
      if (tenantId == null) throw new Error('当前会话缺少租户 ID')
      const result = await deleteProviderAccount(tenantId, account.id, '列表行内删除')
      if (!result.deleted) throw new Error(`账号「${account.name}」删除未确认，请刷新后重试`)
      return `账号「${account.name}」已删除`
    })
  }

  const columns: DataListColumn<AccountTableRow>[] = [
    {
      key: 'name',
      label: '名称',
      render: (row) => (
        <span style={nameCellStyle}>
          <Link to={`/accounts/${row.id}`} style={nameLinkStyle}>{row.name}</Link>
          {row.tags.length > 0 && <span style={tagsStyle}>{row.tags.join(' · ')}</span>}
        </span>
      ),
    },
    { key: 'type', label: '类型', render: (row) => <span className="hk-mono">{row.accountType}</span> },
    { key: 'enabled', label: '启用', badge: true, render: (row) => <StatusBadge tone={row.enabledTone}>{row.enabledText}</StatusBadge> },
    { key: 'health', label: '健康', badge: true, render: (row) => <StatusBadge tone={row.healthTone}>{row.healthState}</StatusBadge> },
    { key: 'credential', label: '凭据', badge: true, render: (row) => <StatusBadge tone={row.credentialTone}>{row.credentialState}</StatusBadge> },
    { key: 'in-flight', label: '在途', render: (row) => <span className="hk-mono" style={numericStyle}>{row.inFlightCount}</span> },
    { key: 'priority', label: '优先级', render: (row) => <span className="hk-mono" style={numericStyle}>{row.priority}</span> },
    { key: 'weight', label: '权重', render: (row) => <span className="hk-mono" style={numericStyle}>{row.staticWeight}</span> },
    { key: 'capacity', label: '并发上限', render: (row) => <span className="hk-mono" style={numericStyle}>{row.capConcurrency}</span> },
    { key: 'last-dispatch', label: '最近派发', render: (row) => <span className="hk-mono">{row.lastDispatchAt}</span> },
  ]

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>账号中心</h1>
          <p className="hk-sub">
            管线第 1 站 · 上游账号池。当前页显示 {visible.length} 条
            {(nameQuery.trim() || healthStateFilter) && items.length !== visible.length ? `(本页内过滤自 ${items.length})` : ''}。
          </p>
        </div>
        <button type="button" onClick={() => setCreateOpen(true)} className="hk-btn hk-btn--green">
          ＋ 新建账号
        </button>
      </header>

      <HealthSummaryCard
        tenantId={tenantId}
        activeHealthState={healthStateFilter}
        refreshNonce={refreshNonce}
        onHealthStateChange={setHealthStateFilter}
      />

      {createOpen && (
        <CreateAccountModal
          onClose={() => setCreateOpen(false)}
          onCreated={refresh}
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
      {tenantId != null && <AccountBulkByTag tenantId={tenantId} onApplied={refresh} />}

      {error && <div style={errorStyle}>{error}</div>}
      {flash && <div style={flashStyle}>{flash}</div>}

      <div className="hk-card">
        {loading && items.length === 0 ? (
          <div style={skeletonWrapStyle}><SkeletonRows rows={6} cols={11} /></div>
        ) : visible.length === 0 ? (
          <EmptyState
            title={cursorStack.length > 1 ? '当前页没有更多账号' : hasFilters ? '没有匹配的账号' : '暂无上游账号'}
            hint={cursorStack.length > 1 ? '返回上一页继续查看。' : hasFilters ? '请调整状态、池组、标签、名称或健康态筛选。' : '新建账号后会在这里显示调度与健康状态。'}
            action={cursorStack.length > 1
              ? { label: '返回上一页', onClick: goPrev }
              : hasFilters
                ? { label: '清除筛选', onClick: resetFilters }
                : { label: '新建账号', onClick: () => setCreateOpen(true) }}
          />
        ) : (
          <div style={{ opacity: loading ? 0.6 : 1, transition: 'opacity .15s' }}>
            <DataListTable
              label="上游账号列表"
              rows={visible}
              rowKey={(row) => row.id}
              columns={columns}
              actions={[
                {
                  label: (row) => row.source.enabled ? '停用' : '启用',
                  onClick: (row) => {
                    void runAction(row.id, '启停账号失败', async () => {
                      if (tenantId == null) throw new Error('当前会话缺少租户 ID')
                      await setAccountEnabled(tenantId, row.id, !row.source.enabled, '列表行内启停')
                      return `账号「${row.name}」已${row.source.enabled ? '停用' : '启用'}`
                    })
                  },
                  disabled: () => busyId !== null,
                },
                {
                  label: '测试',
                  onClick: (row) => {
                    void runAction(row.id, '测试账号失败', async () => {
                      if (tenantId == null) throw new Error('当前会话缺少租户 ID')
                      const result = await testProviderAccount(tenantId, row.id)
                      if (!result.ok) throw new Error(`账号「${row.name}」测试未通过：${result.message || result.error_class || '未知原因'}`)
                      return `账号「${row.name}」测试通过：${result.message || '连通正常'}`
                    })
                  },
                  disabled: () => busyId !== null,
                },
                {
                  label: '清限流',
                  onClick: (row) => {
                    void runAction(row.id, '清除限流失败', async () => {
                      if (tenantId == null) throw new Error('当前会话缺少租户 ID')
                      await clearAccountRateLimit(tenantId, row.id, '列表行内清除限流')
                      return `账号「${row.name}」限流态已清除`
                    })
                  },
                  disabled: () => busyId !== null,
                },
                {
                  label: '删除',
                  onClick: (row) => removeAccount(row.source),
                  tone: 'danger',
                  disabled: () => busyId !== null,
                },
              ]}
            />
          </div>
        )}
      </div>

      <nav aria-label="账号列表分页" style={paginationStyle}>
        <PagerButton disabled={cursorStack.length <= 1 || loading} onClick={goPrev}>
          上一页
        </PagerButton>
        <PagerButton disabled={!nextCursor || loading} onClick={goNext}>
          下一页
        </PagerButton>
        <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>
          第 {cursorStack.length} 页 · 每页 {ACCOUNTS_PAGE_LIMIT}
        </span>
      </nav>
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

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}

function PagerButton({ disabled, onClick, children }: { disabled: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" disabled={disabled} onClick={onClick} className="hk-btn" style={{ opacity: disabled ? 0.5 : 1, cursor: disabled ? 'not-allowed' : 'pointer' }}>
      {children}
    </button>
  )
}

const inputStyle: CSSProperties = {
  height: 32,
  minWidth: 140,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-sm)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
}
const selectStyle: CSSProperties = { ...inputStyle }
const nameCellStyle: CSSProperties = { display: 'flex', flexDirection: 'column', minWidth: 140 }
const nameLinkStyle: CSSProperties = { fontWeight: 600, color: 'var(--hk-primary-700)', textDecoration: 'none' }
const tagsStyle: CSSProperties = { color: 'var(--hk-ink-300)', fontSize: 11 }
const numericStyle: CSSProperties = { display: 'block', textAlign: 'right' }
const errorStyle: CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const flashStyle: CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
const skeletonWrapStyle: CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)' }
const paginationStyle: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }
