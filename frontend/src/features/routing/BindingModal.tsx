import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { createBinding, updateBinding } from './api'
import {
  buildBindingCreate,
  buildBindingUpdate,
  EMPTY_CREATE_BINDING,
  editFormFromBinding,
  FALLBACK_CLASSES,
  hasBindingChanges,
  SELECTION_MODES,
  type BindingCreateForm,
  type BindingEditForm,
} from './selection'
import type { PoolBinding } from './types'

/*
 * 路由绑定 创建/编辑 模态。编辑模式只发改了的字段(buildBindingUpdate);创建模式带 model_id/
 * pool_group_id。selection_mode 选择器是核心——切换严格优先级 / 按权重加权(后端 PR#118)。
 */
export function BindingModal({
  binding,
  onClose,
  onSaved,
}: {
  binding: PoolBinding | null // null=创建
  onClose: () => void
  onSaved: () => void
}) {
  const editing = binding !== null
  const [editForm, setEditForm] = useState<BindingEditForm>(
    binding ? editFormFromBinding(binding) : { priority: '0', weight: '1', selectionMode: 'strict_priority', fallbackClass: 'normal', enabled: true },
  )
  const [createForm, setCreateForm] = useState<BindingCreateForm>(EMPTY_CREATE_BINDING)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const selMode = editing ? editForm.selectionMode : createForm.selectionMode
  const setSelMode = (v: string) => (editing ? setEditForm((f) => ({ ...f, selectionMode: v })) : setCreateForm((f) => ({ ...f, selectionMode: v })))
  const selHint = SELECTION_MODES.find((m) => m.value === selMode)?.hint

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      if (editing && binding) {
        if (!hasBindingChanges(binding, editForm)) {
          setError('未修改任何字段')
          setBusy(false)
          return
        }
        // 回填全字段(后端 PATCH 是整行覆盖,只发 diff 会重置省略字段)。
        await updateBinding(binding.id, buildBindingUpdate(binding, editForm))
      } else {
        const built = buildBindingCreate(createForm)
        if ('error' in built) {
          setError(built.error)
          setBusy(false)
          return
        }
        await createBinding(built)
      }
      onSaved()
      onClose()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Overlay onClose={onClose}>
      <div style={panel} onClick={(e) => e.stopPropagation()}>
        <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <h2 style={{ fontSize: 18 }}>{editing ? `编辑绑定 #${binding!.id}` : '新建路由绑定'}</h2>
          <button type="button" onClick={onClose} style={iconBtn} aria-label="关闭">
            ✕
          </button>
        </header>

        {editing ? (
          <div style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
            model #{binding!.model_id} → pool #{binding!.pool_group_id}
          </div>
        ) : (
          <>
            <Field label="model_id">
              <input value={createForm.modelId} onChange={(e) => setCreateForm((f) => ({ ...f, modelId: e.target.value }))} inputMode="numeric" style={inp} />
            </Field>
            <Field label="pool_group_id">
              <input value={createForm.poolGroupId} onChange={(e) => setCreateForm((f) => ({ ...f, poolGroupId: e.target.value }))} inputMode="numeric" style={inp} />
            </Field>
          </>
        )}

        <Field label="优先级">
          <input
            value={editing ? editForm.priority : createForm.priority}
            onChange={(e) => (editing ? setEditForm((f) => ({ ...f, priority: e.target.value })) : setCreateForm((f) => ({ ...f, priority: e.target.value })))}
            inputMode="numeric"
            style={inp}
          />
        </Field>
        <Field label="权重(priority_weighted 时生效)">
          <input
            value={editing ? editForm.weight : createForm.weight}
            onChange={(e) => (editing ? setEditForm((f) => ({ ...f, weight: e.target.value })) : setCreateForm((f) => ({ ...f, weight: e.target.value })))}
            inputMode="numeric"
            style={inp}
          />
        </Field>
        <Field label="选号策略">
          <select value={selMode} onChange={(e) => setSelMode(e.target.value)} style={inp}>
            {SELECTION_MODES.map((m) => (
              <option key={m.value} value={m.value}>
                {m.label}
              </option>
            ))}
          </select>
          {selHint && <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{selHint}</span>}
        </Field>
        <Field label="兜底类">
          <select
            value={editing ? editForm.fallbackClass : createForm.fallbackClass}
            onChange={(e) => (editing ? setEditForm((f) => ({ ...f, fallbackClass: e.target.value })) : setCreateForm((f) => ({ ...f, fallbackClass: e.target.value })))}
            style={inp}
          >
            {FALLBACK_CLASSES.map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </select>
        </Field>
        {editing && (
          <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 13, color: 'var(--hk-ink-700)' }}>
            <input type="checkbox" checked={editForm.enabled} onChange={(e) => setEditForm((f) => ({ ...f, enabled: e.target.checked }))} />
            启用该绑定
          </label>
        )}

        {error && <Banner>{error}</Banner>}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} style={ghost}>
            取消
          </button>
          <button type="button" disabled={busy} onClick={submit} style={primary}>
            {busy ? '保存中…' : editing ? '保存' : '创建'}
          </button>
        </div>
      </div>
    </Overlay>
  )
}

function Overlay({ children, onClose }: { children: React.ReactNode; onClose: () => void }) {
  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }}>
      {children}
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
function Banner({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{children}</div>
}

const panel: React.CSSProperties = { width: 'min(480px, 100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const baseBtn: React.CSSProperties = { height: 34, padding: '0 var(--hk-space-4)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const primary: React.CSSProperties = { ...baseBtn, border: '1px solid var(--hk-primary-600)', background: 'var(--hk-primary-500)', color: '#fff' }
const ghost: React.CSSProperties = { ...baseBtn, border: '1px solid var(--hk-line)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontWeight: 400 }
const iconBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-ink-500)', fontSize: 16, cursor: 'pointer' }
