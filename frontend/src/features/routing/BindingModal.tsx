import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { createBinding, updateBinding } from './api'
import {
  buildBindingCreate,
  buildBindingUpdate,
  EMPTY_CREATE_BINDING,
  editFormFromBinding,
  FALLBACK_CLASSES,
  fallbackClassError,
  hasBindingChanges,
  isFallbackClass,
  maxParallelRequestsError,
  SELECTION_MODES,
  type BindingCreateForm,
  type BindingEditForm,
} from './selection'
import type { PoolBinding } from './types'

/*
 * 路由绑定创建/编辑模态。编辑模式回填仍由界面管理的有效字段；创建模式带 model_id/
 * pool_group_id。selection_mode 与 fallback_class 都是运行时字段，创建和编辑必须显式提交。
 */
export function BindingModal({
  tenantId,
  binding,
  onClose,
  onSaved,
}: {
  tenantId: number
  binding: PoolBinding | null // null=创建
  onClose: () => void
  onSaved: () => void
}) {
  const editing = binding !== null
  const [editForm, setEditForm] = useState<BindingEditForm>(
    binding
      ? editFormFromBinding(binding)
      : { priority: '0', selectionMode: 'strict_priority', fallbackClass: 'normal', maxParallelRequests: '', enabled: true },
  )
  const [createForm, setCreateForm] = useState<BindingCreateForm>(EMPTY_CREATE_BINDING)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const selMode = editing ? editForm.selectionMode : createForm.selectionMode
  const setSelMode = (v: string) => (editing ? setEditForm((f) => ({ ...f, selectionMode: v })) : setCreateForm((f) => ({ ...f, selectionMode: v })))
  const selHint = SELECTION_MODES.find((m) => m.value === selMode)?.hint
  const fallbackClass = editing ? editForm.fallbackClass : createForm.fallbackClass
  const setFallbackClass = (value: string) => {
    if (!isFallbackClass(value)) {
      setError(fallbackClassError(value)!)
      return
    }
    if (editing) setEditForm((form) => ({ ...form, fallbackClass: value }))
    else setCreateForm((form) => ({ ...form, fallbackClass: value }))
  }
  const fallbackHint = FALLBACK_CLASSES.find((item) => item.value === fallbackClass)?.hint

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      if (editing && binding) {
        const validationError = fallbackClassError(editForm.fallbackClass) ?? maxParallelRequestsError(editForm.maxParallelRequests)
        if (validationError) {
          setError(validationError)
          setBusy(false)
          return
        }
        if (!hasBindingChanges(binding, editForm)) {
          setError('未修改任何字段')
          setBusy(false)
          return
        }
        // fallback_class 即使没改也回填，防止后端整行覆盖把非 normal 静默重置。
        await updateBinding(binding.id, buildBindingUpdate(binding, editForm), tenantId)
      } else {
        const built = buildBindingCreate(createForm)
        if ('error' in built) {
          setError(built.error)
          setBusy(false)
          return
        }
        await createBinding(built, tenantId)
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
        <Field label="降级类 (fallback_class)">
          <select value={fallbackClass} onChange={(e) => setFallbackClass(e.target.value)} style={inp}>
            {FALLBACK_CLASSES.map((item) => (
              <option key={item.value} value={item.value}>
                {item.label}
              </option>
            ))}
          </select>
          {fallbackHint && <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{fallbackHint}</span>}
        </Field>
        <Field label="最大并发请求数">
          <input
            value={editing ? editForm.maxParallelRequests : createForm.maxParallelRequests}
            onChange={(e) =>
              editing
                ? setEditForm((f) => ({ ...f, maxParallelRequests: e.target.value }))
                : setCreateForm((f) => ({ ...f, maxParallelRequests: e.target.value }))
            }
            type="number"
            inputMode="numeric"
            min={0}
            placeholder="留空表示不限"
            style={inp}
          />
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>0 或留空表示不限；正整数限制该绑定的全局在途请求数</span>
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
