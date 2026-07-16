import { useCallback, useEffect, useState, type CSSProperties } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { SkeletonRows } from '../../ui/Skeleton'
import { StatCard } from '../../ui/StatCard'
import { StatusBadge } from '../../ui/StatusBadge'
import { batchRevokeApiKeys, listApiKeys, revokeApiKey } from './api'
import { buildBatchRevoke, isSelectable, summarizeBatchResult, togglePageSelection, toggleSelected } from './batch'
import { CreateKeyModal } from './CreateKeyModal'
import { EditKeyModal } from './EditKeyModal'
import {
  KEYS_PAGE_LIMIT,
  KEYS_PAGE_LIMIT_OPTIONS,
  mapKeyPagination,
  mapKeyRows,
  mapKeyStats,
  type KeyTableRow,
} from './keys'
import type { ApiKeyView } from './types'

/*
 * API Key · 我的密钥(P0)。/v1/api-keys 使用 session 鉴权，只管理当前用户自己的 Key。
 * 列表只展示 key_prefix(脱敏)，绝不回显明文；全量总数直接使用后端 count。
 */
export function KeysPage() {
  const [keys, setKeys] = useState<ApiKeyView[]>([])
  const [totalCount, setTotalCount] = useState<number | null>(null)
  const [offset, setOffset] = useState(0)
  const [limit, setLimit] = useState(KEYS_PAGE_LIMIT)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<ApiKeyView | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)
  const [flash, setFlash] = useState<string | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    setKeys([])
    setTotalCount(null)
    listApiKeys(offset, limit, signal)
      .then((resp) => {
        setKeys(resp.api_keys)
        setTotalCount(resp.count)
        // 刷新后只保留当前页仍为活跃态的选择，避免对失效 Key 发批量请求。
        setSelected((current) => {
          const activeIds = new Set(resp.api_keys.filter(isSelectable).map((key) => key.api_key_id))
          return new Set([...current].filter((id) => activeIds.has(id)))
        })
      })
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载密钥列表失败')
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [limit, offset])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  // 翻页时选择范围切到新页面，避免批量条显示不可见的旧页项目。
  useEffect(() => setSelected(new Set()), [limit, offset])

  const refresh = () => setRefreshNonce((nonce) => nonce + 1)

  const revoke = async (key: ApiKeyView) => {
    if (!window.confirm(`确认撤销 Key「${key.name}」(${key.key_prefix})?撤销后不可恢复。`)) return
    setBusyId(key.api_key_id)
    setError(null)
    try {
      await revokeApiKey(key.api_key_id, '')
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '撤销失败')
    } finally {
      setBusyId(null)
    }
  }

  const batchRevoke = async () => {
    const ids = [...selected]
    const built = buildBatchRevoke(ids, '')
    if ('error' in built) {
      setError(built.error)
      return
    }
    if (!window.confirm(`确认批量撤销 ${ids.length} 个 Key?撤销后不可恢复。`)) return
    setBatchBusy(true)
    setError(null)
    setFlash(null)
    try {
      const resp = await batchRevokeApiKeys(built.ids, built.reason)
      setFlash(summarizeBatchResult(resp))
      setSelected(new Set())
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '批量撤销失败')
    } finally {
      setBatchBusy(false)
    }
  }

  const rows = mapKeyRows(keys)
  const stats = mapKeyStats(keys, totalCount)
  const pagination = mapKeyPagination({ offset, limit, returnedCount: keys.length, totalCount })
  const totalCopy = totalCount === null
    ? loading ? '总数加载中。' : '总数暂不可用。'
    : `共 ${totalCount.toLocaleString('zh-CN')} 个。`
  const columns: DataListColumn<KeyTableRow>[] = [
    { key: 'name', label: '名称', render: (row) => <span style={nameStyle}>{row.name}</span> },
    { key: 'prefix', label: '前缀', render: (row) => <code className="hk-mono">{row.prefix}</code> },
    {
      key: 'status',
      label: '状态',
      badge: true,
      render: (row) => <StatusBadge tone={row.statusTone}>{row.statusText}</StatusBadge>,
    },
    { key: 'expires', label: '过期', render: (row) => <span className="hk-mono">{row.expiresAt}</span> },
    { key: 'last-used', label: '最近使用', render: (row) => <span className="hk-mono">{row.lastUsedAt}</span> },
    { key: 'created', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
  ]

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>我的密钥</h1>
          <p className="hk-sub">管线第 3 站 · 把账号池签发成可用密钥。{totalCopy}</p>
        </div>
        <button type="button" onClick={() => setCreateOpen(true)} className="hk-btn hk-btn--green">
          ＋ 新建 Key
        </button>
      </header>

      {createOpen && <CreateKeyModal onClose={() => setCreateOpen(false)} onCreated={refresh} />}
      {editing && (
        <EditKeyModal
          apiKey={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            refresh()
          }}
        />
      )}

      <section aria-label="密钥统计" style={statsGridStyle}>
        {stats.map((card) => (
          <StatCard key={card.label} label={card.label} value={card.value} hint={card.hint} tone={card.tone} />
        ))}
      </section>

      {error && <div style={errorStyle}>{error}</div>}
      {flash && <div style={flashStyle}>{flash}</div>}

      {selected.size > 0 && (
        <div style={batchBarStyle}>
          <span style={{ fontSize: 13, color: 'var(--hk-ink-700)' }}>已选 {selected.size} 个</span>
          <button type="button" disabled={batchBusy} onClick={() => void batchRevoke()} className="hk-btn hk-btn--danger hk-btn--sm">
            {batchBusy ? '撤销中…' : '批量撤销'}
          </button>
          <button type="button" onClick={() => setSelected(new Set())} style={clearSelectionStyle}>清空选择</button>
        </div>
      )}

      <div className="hk-card">
        {loading ? (
          <div style={skeletonWrapStyle}><SkeletonRows rows={6} cols={8} /></div>
        ) : rows.length === 0 ? (
          <EmptyState
            title={offset > 0 ? '当前页没有更多密钥' : '暂无密钥'}
            hint={offset > 0 ? '返回上一页继续查看。' : '新建 Key 后会在这里显示脱敏前缀与使用状态。'}
            action={offset > 0
              ? { label: '返回上一页', onClick: () => setOffset(Math.max(0, offset - limit)) }
              : { label: '新建 Key', onClick: () => setCreateOpen(true) }}
          />
        ) : (
          <DataListTable
            label="我的密钥列表"
            rows={rows}
            rowKey={(row) => row.id}
            columns={columns}
            selectable={{
              selectedIds: selected,
              onToggle: (id) => {
                if (typeof id === 'number') setSelected((current) => toggleSelected(current, id))
              },
              onToggleAll: (ids) => {
                const pageIds = ids.filter((id): id is number => typeof id === 'number')
                setSelected((current) => togglePageSelection(current, pageIds))
              },
              isSelectable: (row) => isSelectable(row.source),
            }}
            actions={[
              {
                label: (row) => row.status === 'active' ? '编辑' : '管理',
                onClick: (row) => setEditing(row.source),
                disabled: (row) => busyId === row.id,
              },
              {
                label: '撤销',
                onClick: (row) => { void revoke(row.source) },
                tone: 'danger',
                disabled: (row) => !isSelectable(row.source) || busyId === row.id,
              },
            ]}
          />
        )}
      </div>

      <nav aria-label="密钥列表分页" style={paginationStyle}>
        <span>{pagination.scopeText} · 第 {pagination.page} 页</span>
        <div style={paginationActionsStyle}>
          <label style={pageSizeLabelStyle}>
            每页
            <select
              aria-label="每页密钥数"
              value={limit}
              disabled={loading}
              onChange={(event) => { setLimit(Number(event.target.value)); setOffset(0) }}
              className="hk-input"
              style={pageSizeSelectStyle}
            >
              {KEYS_PAGE_LIMIT_OPTIONS.map((size) => <option key={size} value={size}>{size} 个</option>)}
            </select>
          </label>
          <button type="button" className="hk-btn hk-btn--sm" disabled={loading || !pagination.canPrevious} onClick={() => setOffset(Math.max(0, offset - limit))}>上一页</button>
          <button type="button" className="hk-btn hk-btn--sm" disabled={loading || !pagination.canNext} onClick={() => setOffset(offset + limit)}>下一页</button>
        </div>
      </nav>
    </div>
  )
}

const statsGridStyle: CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))', gap: 'var(--hk-space-3)' }
const nameStyle: CSSProperties = { fontWeight: 600, color: 'var(--hk-ink-900)' }
const errorStyle: CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const flashStyle: CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
const batchBarStyle: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-2) var(--hk-space-4)', background: 'var(--hk-surface-sunken)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)' }
const clearSelectionStyle: CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-ink-500)', fontSize: 13, cursor: 'pointer' }
const skeletonWrapStyle: CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)' }
const paginationStyle: CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 'var(--hk-space-3)', fontSize: 13, color: 'var(--hk-ink-500)' }
const paginationActionsStyle: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }
const pageSizeLabelStyle: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }
const pageSizeSelectStyle: CSSProperties = { width: 92, minHeight: 30, padding: '0 var(--hk-space-2)' }
