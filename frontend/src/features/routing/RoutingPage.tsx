import { useCallback, useEffect, useState } from 'react'
import { useMe } from '../../auth/me'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { deleteBinding, listBindings } from './api'
import { BindingModal } from './BindingModal'
import { mapBindingRows, type BindingTableRow } from './selection'
import type { PoolBinding } from './types'

/*
 * 路由与池 · 模型→池路由绑定(P0)。管线第 2 站。
 * /admin/v1/model-pool-bindings 列表(可按 model_id/pool_group_id 筛选)+ 创建 + 编辑选号策略 + 删除。
 * 选号策略(strict_priority / priority_weighted)是核心；加权模式读取账号 static_weight。
 */
export function RoutingPage() {
  const tenantId = useMe().tenantId
  const [bindings, setBindings] = useState<PoolBinding[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [draftModel, setDraftModel] = useState('')
  const [draftPool, setDraftPool] = useState('')
  const [filters, setFilters] = useState<{ modelId: string; poolGroupId: string }>({ modelId: '', poolGroupId: '' })
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [modal, setModal] = useState<{ open: boolean; binding: PoolBinding | null }>({ open: false, binding: null })
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(
    (signal: AbortSignal) => {
      if (tenantId == null) return
      setLoading(true)
      setError(null)
      listBindings(tenantId, filters, signal)
        .then((resp) => setBindings(resp.items))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载路由绑定失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [filters, tenantId],
  )

  useEffect(() => {
    if (tenantId == null) return
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const refresh = () => setRefreshNonce((n) => n + 1)
  const rows = mapBindingRows(bindings)

  const remove = async (b: PoolBinding) => {
    if (!window.confirm(`确认删除绑定 #${b.id}(model #${b.model_id} → pool #${b.pool_group_id})?`)) return
    setBusyId(b.id)
    setError(null)
    try {
      if (tenantId == null) return
      await deleteBinding(b.id, tenantId)
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '删除失败')
    } finally {
      setBusyId(null)
    }
  }

  if (tenantId == null) {
    return <EmptyState title="正在加载租户上下文" hint="请稍候。" />
  }

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>路由与池管理</h1>
          <p className="hk-sub">
            管线第 2 站 · 模型→池路由绑定与选号策略。共 {bindings.length} 条。
          </p>
        </div>
        <button type="button" onClick={() => setModal({ open: true, binding: null })} className="hk-btn hk-btn--green">
          ＋ 新建绑定
        </button>
      </header>

      {modal.open && <BindingModal tenantId={tenantId} binding={modal.binding} onClose={() => setModal({ open: false, binding: null })} onSaved={refresh} />}

      <form
        onSubmit={(e) => {
          e.preventDefault()
          setFilters({ modelId: draftModel, poolGroupId: draftPool })
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)', flexWrap: 'wrap' }}
      >
        <Field label="model_id">
          <input value={draftModel} onChange={(e) => setDraftModel(e.target.value)} inputMode="numeric" placeholder="筛选模型" style={inp} />
        </Field>
        <Field label="pool_group_id">
          <input value={draftPool} onChange={(e) => setDraftPool(e.target.value)} inputMode="numeric" placeholder="筛选池组" style={inp} />
        </Field>
        <button type="submit" className="hk-btn hk-btn--green">
          查询
        </button>
        <button
          type="button"
          onClick={() => {
            setDraftModel('')
            setDraftPool('')
            setFilters({ modelId: '', poolGroupId: '' })
          }}
          className="hk-btn"
        >
          重置
        </button>
      </form>

      {error && <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{error}</div>}

      <div className="hk-card">
        {loading && bindings.length === 0 ? (
          <EmptyState title="正在加载路由绑定" hint="请稍候。" />
        ) : bindings.length === 0 ? (
          <EmptyState title="没有路由绑定" hint="点击「新建绑定」把模型挂到池组。" />
        ) : (
          <DataListTable
            label="路由绑定列表"
            rows={rows}
            rowKey={(row) => row.id}
            columns={bindingColumns}
            actions={[
              { label: '编辑', disabled: (row) => busyId === row.id, onClick: (row) => setModal({ open: true, binding: row.binding }) },
              { label: '删除', tone: 'danger', disabled: (row) => busyId === row.id, onClick: (row) => void remove(row.binding) },
            ]}
          />
        )}
      </div>
    </div>
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

export const bindingColumns: DataListColumn<BindingTableRow>[] = [
  { key: 'model', label: '模型', render: (row) => <span className="hk-mono">{row.model}</span> },
  { key: 'pool', label: '池组', render: (row) => <span className="hk-mono">{row.pool}</span> },
  { key: 'priority', label: '优先级', render: (row) => <span className="hk-mono">{row.priority}</span> },
  { key: 'selection-mode', label: '选号策略', badge: true, render: (row) => <StatusBadge tone={row.selectionTone}>{row.selectionMode}</StatusBadge> },
  { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
]

const inp: React.CSSProperties = { height: 32, minWidth: 140, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)' }
