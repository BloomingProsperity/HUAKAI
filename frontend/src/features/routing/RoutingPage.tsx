import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { deleteBinding, listBindings } from './api'
import { BindingModal } from './BindingModal'
import { fallbackClassLabel, selectionModeLabel } from './selection'
import type { PoolBinding } from './types'

/*
 * 路由与池 · 模型→池路由绑定(P0)。管线第 2 站。
 * /admin/v1/model-pool-bindings 列表(可按 model_id/pool_group_id 筛选)+ 创建 + 编辑选号策略 + 删除。
 * 选号策略(strict_priority / priority_weighted)是核心,对应后端 PR#118 加权选号。
 */
export function RoutingPage() {
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
      setLoading(true)
      setError(null)
      listBindings(filters, signal)
        .then((resp) => setBindings(resp.items))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载路由绑定失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [filters],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const refresh = () => setRefreshNonce((n) => n + 1)

  const remove = async (b: PoolBinding) => {
    if (!window.confirm(`确认删除绑定 #${b.id}(model #${b.model_id} → pool #${b.pool_group_id})?`)) return
    setBusyId(b.id)
    setError(null)
    try {
      await deleteBinding(b.id)
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '删除失败')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>路由与池管理</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            管线第 2 站 · 模型→池路由绑定与选号策略。共 {bindings.length} 条。
          </p>
        </div>
        <button type="button" onClick={() => setModal({ open: true, binding: null })} style={newBtn}>
          ＋ 新建绑定
        </button>
      </header>

      {modal.open && <BindingModal binding={modal.binding} onClose={() => setModal({ open: false, binding: null })} onSaved={refresh} />}

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
        <button type="submit" style={primaryBtn}>
          查询
        </button>
        <button
          type="button"
          onClick={() => {
            setDraftModel('')
            setDraftPool('')
            setFilters({ modelId: '', poolGroupId: '' })
          }}
          style={ghostBtn}
        >
          重置
        </button>
      </form>

      {error && <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{error}</div>}

      <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }}>
        {loading && bindings.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : bindings.length === 0 ? (
          <Empty>没有路由绑定。点击「新建绑定」把模型挂到池组。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['模型', '池组', '优先级', '权重', '选号策略', '兜底类', '状态', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {bindings.map((b) => (
                  <tr key={b.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={tdMono}>#{b.model_id}</td>
                    <td style={tdMono}>#{b.pool_group_id}</td>
                    <td style={tdNum}>{b.priority}</td>
                    <td style={tdNum}>{b.weight}</td>
                    <td style={td}>
                      <StatusBadge tone={b.selection_mode === 'priority_weighted' ? 'info' : 'muted'}>
                        {selectionModeLabel(b.selection_mode)}
                      </StatusBadge>
                    </td>
                    <td style={td}>{fallbackClassLabel(b.fallback_class)}</td>
                    <td style={td}>
                      <StatusBadge tone={b.enabled ? 'ok' : 'muted'}>{b.enabled ? '启用' : '停用'}</StatusBadge>
                    </td>
                    <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                      <button type="button" onClick={() => setModal({ open: true, binding: b })} style={linkBtn}>
                        编辑
                      </button>
                      <button type="button" disabled={busyId === b.id} onClick={() => remove(b)} style={{ ...linkBtn, color: 'var(--hk-danger)' }}>
                        删除
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
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
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const inp: React.CSSProperties = { height: 32, minWidth: 140, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)' }
const tdNum: React.CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const newBtn: React.CSSProperties = { height: 36, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 14, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
