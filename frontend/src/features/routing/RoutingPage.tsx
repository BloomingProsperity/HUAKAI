import { useCallback, useEffect, useState } from 'react'
import { useMe } from '../../auth/me'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { deleteBinding, listBindings } from './api'
import { BindingModal } from './BindingModal'
import { RoutingOverridesPanel } from './RoutingOverridesPanel'
import {
  enabledModelIdsWithoutNormal,
  FALLBACK_CLASSES,
  filterBindingRows,
  isFallbackClass,
  mapBindingRows,
  type BindingTableRow,
} from './selection'
import type { FallbackClass, PoolBinding } from './types'

type RoutingTab = 'bindings' | 'overrides'

/*
 * 路由与池 · 模型→池路由绑定(P0)。管线第 2 站。
 * /admin/v1/model-pool-bindings 列表(可按 model_id/pool_group_id/class 筛选)+ 创建 + 编辑 + 删除。
 * class 筛选仅在已加载数据上执行，避免伪造后端未声明的查询参数。
 */
export function RoutingPage() {
  const tenantId = useMe().tenantId
  const [tab, setTab] = useState<RoutingTab>('bindings')
  const [bindings, setBindings] = useState<PoolBinding[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [draftModel, setDraftModel] = useState('')
  const [draftPool, setDraftPool] = useState('')
  const [draftFallbackClass, setDraftFallbackClass] = useState<FallbackClass | ''>('')
  const [filters, setFilters] = useState<{ modelId: string; poolGroupId: string; fallbackClass: FallbackClass | '' }>({
    modelId: '',
    poolGroupId: '',
    fallbackClass: '',
  })
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [modal, setModal] = useState<{ open: boolean; binding: PoolBinding | null }>({ open: false, binding: null })
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(
    (signal: AbortSignal) => {
      if (tenantId == null) return
      setLoading(true)
      setError(null)
      listBindings(tenantId, { modelId: filters.modelId, poolGroupId: filters.poolGroupId }, signal)
        .then((resp) => setBindings(resp.items))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载路由绑定失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [filters.modelId, filters.poolGroupId, tenantId],
  )

  useEffect(() => {
    if (tenantId == null || tab !== 'bindings') return
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce, tab, tenantId])

  const refresh = () => setRefreshNonce((n) => n + 1)
  const rows = filterBindingRows(mapBindingRows(bindings), filters.fallbackClass)
  const missingNormalModelIDs = filters.poolGroupId === '' ? enabledModelIdsWithoutNormal(bindings) : []

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
            {tab === 'bindings'
              ? `管线第 2 站 · 模型→池路由绑定、选号策略与降级类。共 ${bindings.length} 条${filters.fallbackClass ? `，当前显示 ${rows.length} 条` : ''}。`
              : '池组内模型→账号候选强制 pin；命中后只保留指定账号子集。'}
          </p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
          <RoutingTabs value={tab} onChange={setTab} />
          {tab === 'bindings' && (
            <button type="button" onClick={() => setModal({ open: true, binding: null })} className="hk-btn hk-btn--green">
              ＋ 新建绑定
            </button>
          )}
        </div>
      </header>

      {tab === 'overrides' ? (
        <RoutingOverridesPanel tenantId={tenantId} />
      ) : (
      <>
        {modal.open && <BindingModal tenantId={tenantId} binding={modal.binding} onClose={() => setModal({ open: false, binding: null })} onSaved={refresh} />}

      <form
        onSubmit={(e) => {
          e.preventDefault()
          setFilters({ modelId: draftModel, poolGroupId: draftPool, fallbackClass: draftFallbackClass })
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)', flexWrap: 'wrap' }}
      >
        <Field label="model_id">
          <input value={draftModel} onChange={(e) => setDraftModel(e.target.value)} inputMode="numeric" placeholder="筛选模型" style={inp} />
        </Field>
        <Field label="pool_group_id">
          <input value={draftPool} onChange={(e) => setDraftPool(e.target.value)} inputMode="numeric" placeholder="筛选池组" style={inp} />
        </Field>
        <Field label="fallback_class">
          <select
            value={draftFallbackClass}
            onChange={(e) => {
              const value = e.target.value
              if (value === '' || isFallbackClass(value)) setDraftFallbackClass(value)
            }}
            style={inp}
          >
            <option value="">全部类别</option>
            {FALLBACK_CLASSES.map((item) => (
              <option key={item.value} value={item.value}>
                {item.label}
              </option>
            ))}
          </select>
        </Field>
        <button type="submit" className="hk-btn hk-btn--green">
          查询
        </button>
        <button
          type="button"
          onClick={() => {
            setDraftModel('')
            setDraftPool('')
            setDraftFallbackClass('')
            setFilters({ modelId: '', poolGroupId: '', fallbackClass: '' })
          }}
          className="hk-btn"
        >
          重置
        </button>
      </form>

      {error && <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{error}</div>}

      {!loading && !error && missingNormalModelIDs.length > 0 && (
        <div style={warningBanner} role="status">
          当前启用绑定中缺少 normal 主类的模型：{missingNormalModelIDs.map((id) => `#${id}`).join('、')}。这些模型不会把降级类隐式晋升为主类。
        </div>
      )}

      <div className="hk-card">
        {loading && bindings.length === 0 ? (
          <EmptyState title="正在加载路由绑定" hint="请稍候。" />
        ) : bindings.length === 0 ? (
          <EmptyState title="没有路由绑定" hint="点击「新建绑定」把模型挂到池组。" />
        ) : rows.length === 0 ? (
          <EmptyState title="没有符合类别筛选的绑定" hint="调整 fallback_class 筛选或点击重置。" />
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
      </>
      )}
    </div>
  )
}

export function RoutingTabs({ value, onChange }: { value: RoutingTab; onChange: (value: RoutingTab) => void }) {
  return (
    <div className="hk-seg" role="tablist" aria-label="路由配置类型">
      {([
        { value: 'bindings', label: '绑定' },
        { value: 'overrides', label: '强制 pin' },
      ] as const).map((option) => (
        <button
          key={option.value}
          type="button"
          role="tab"
          aria-selected={value === option.value}
          className={value === option.value ? 'is-on' : undefined}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
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
  {
    key: 'fallback-class',
    label: '降级类',
    badge: true,
    render: (row) => (
      <span title={row.fallbackClassHint}>
        <StatusBadge tone={row.fallbackClassTone}>{row.fallbackClassLabel}</StatusBadge>
      </span>
    ),
  },
  { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
]

const inp: React.CSSProperties = { height: 32, minWidth: 140, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)' }
const warningBanner: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-ink-700)', background: 'var(--hk-warn-soft)', border: '1px solid var(--hk-warn)' }
