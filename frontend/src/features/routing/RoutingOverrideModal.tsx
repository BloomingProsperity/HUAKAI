import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { createRoutingOverride, updateRoutingOverride } from './api'
import {
  buildRoutingOverrideCreate,
  buildRoutingOverrideUpdate,
  editRoutingOverrideForm,
  EMPTY_ROUTING_OVERRIDE_FORM,
  type RoutingOverrideForm,
} from './routingOverrideSelection'
import type { ModelRoutingOverride } from './types'

export function RoutingOverrideModal({
  tenantId,
  item,
  onClose,
  onSaved,
}: {
  tenantId: number
  item: ModelRoutingOverride | null
  onClose: () => void
  onSaved: () => void
}) {
  const editing = item !== null
  const [form, setForm] = useState<RoutingOverrideForm>(item ? editRoutingOverrideForm(item) : EMPTY_ROUTING_OVERRIDE_FORM)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const setField = <K extends keyof RoutingOverrideForm>(key: K, value: RoutingOverrideForm[K]) => {
    setForm((current) => ({ ...current, [key]: value }))
  }

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      if (editing && item) {
        const request = buildRoutingOverrideUpdate(form)
        if ('error' in request) {
          setError(request.error)
          return
        }
        await updateRoutingOverride(item.id, request, tenantId)
      } else {
        const request = buildRoutingOverrideCreate(form)
        if ('error' in request) {
          setError(request.error)
          return
        }
        await createRoutingOverride(request, tenantId)
      }
      onSaved()
      onClose()
    } catch (caught) {
      setError(caught instanceof ApiError ? `${caught.message}(${caught.code})` : '保存强制 pin 失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div role="presentation" style={overlay} onClick={onClose}>
      <section role="dialog" aria-modal="true" aria-label={editing ? '编辑强制 pin' : '新建强制 pin'} style={panel} onClick={(event) => event.stopPropagation()}>
        <header style={header}>
          <h2 style={{ margin: 0, fontSize: 18 }}>{editing ? `编辑强制 pin #${item!.id}` : '新建强制 pin'}</h2>
          <button type="button" onClick={onClose} style={iconButton} aria-label="关闭">✕</button>
        </header>

        {editing ? (
          <div style={immutableHint}>模型 {item!.model} · pool #{item!.pool_group_id}；修改模型或池组请删除后重建。</div>
        ) : (
          <>
            <Field label="pool_group_id">
              <input value={form.poolGroupId} onChange={(event) => setField('poolGroupId', event.target.value)} inputMode="numeric" style={input} />
            </Field>
            <Field label="模型名">
              <input value={form.model} onChange={(event) => setField('model', event.target.value)} placeholder="例如 gpt-4.1" style={input} />
            </Field>
          </>
        )}

        <Field label="provider_account_ids">
          <textarea
            value={form.providerAccountIDs}
            onChange={(event) => setField('providerAccountIDs', event.target.value)}
            placeholder="例如 11, 13, 17"
            rows={3}
            style={{ ...input, height: 'auto', minHeight: 72, paddingTop: 8, resize: 'vertical' }}
          />
          <span style={hint}>逗号或空格分隔；只允许正整数，重复 ID 会按首现顺序去重。</span>
        </Field>

        <label style={checkboxLabel}>
          <input type="checkbox" checked={form.enabled} onChange={(event) => setField('enabled', event.target.checked)} />
          立即启用
        </label>

        {error && <div style={errorBox}>{error}</div>}
        <footer style={footer}>
          <button type="button" className="hk-btn" onClick={onClose} disabled={busy}>取消</button>
          <button type="button" className="hk-btn hk-btn--green" onClick={() => void submit()} disabled={busy}>
            {busy ? '保存中…' : '保存'}
          </button>
        </footer>
      </section>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return <label style={field}><span>{label}</span>{children}</label>
}

const overlay: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 80, display: 'grid', placeItems: 'center', padding: 20, background: 'rgba(15, 23, 42, 0.48)' }
const panel: React.CSSProperties = { width: 'min(520px, 100%)', maxHeight: 'calc(100vh - 40px)', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)', padding: 'var(--hk-space-5)', borderRadius: 'var(--hk-radius-lg)', background: 'var(--hk-surface)', boxShadow: 'var(--hk-shadow-3)' }
const header: React.CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }
const iconButton: React.CSSProperties = { border: 0, background: 'transparent', color: 'var(--hk-ink-500)', cursor: 'pointer', fontSize: 16 }
const field: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 5, color: 'var(--hk-ink-500)', fontSize: 12 }
const input: React.CSSProperties = { height: 36, boxSizing: 'border-box', padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontSize: 13 }
const hint: React.CSSProperties = { color: 'var(--hk-ink-300)', fontSize: 11, lineHeight: 1.5 }
const immutableHint: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface-sunken)', color: 'var(--hk-ink-500)', fontSize: 12 }
const checkboxLabel: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 8, color: 'var(--hk-ink-700)', fontSize: 13 }
const errorBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)', border: '1px solid var(--hk-danger-soft)', background: 'var(--hk-danger-soft)', color: 'var(--hk-danger)', fontSize: 13 }
const footer: React.CSSProperties = { display: 'flex', justifyContent: 'flex-end', gap: 'var(--hk-space-2)' }
