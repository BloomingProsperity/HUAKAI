import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { updateProviderAccount } from './api'
import { buildAccountUpdate, formFromAccount, type AccountEditForm } from './edit'
import type { ProviderAccount } from './types'

/*
 * 账号参数编辑模态(P1)。PATCH /admin/v1/provider-accounts/{id} 改池调优旋钮:
 * 优先级 / 静态权重 / 并发上限 / 标签。仅下发改动字段(buildAccountUpdate);无改动时不发请求。
 */
export function EditAccountModal({
  account,
  onClose,
  onSaved,
}: {
  account: ProviderAccount
  onClose: () => void
  onSaved: (updated: ProviderAccount) => void
}) {
  const [form, setForm] = useState<AccountEditForm>(formFromAccount(account))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const set = <K extends keyof AccountEditForm>(k: K, v: AccountEditForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    const built = buildAccountUpdate(account, form)
    if ('error' in built) {
      setError(built.error)
      return
    }
    if ('noop' in built) {
      onClose()
      return
    }
    setBusy(true)
    setError(null)
    try {
      const updated = await updateProviderAccount(account.id, built)
      onSaved(updated)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 'min(460px,100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <h2 style={{ fontSize: 18 }}>编辑账号参数</h2>
        <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }}>仅保存改动的字段;留空标签即清空。</p>
        <Field label="优先级(priority,越小越先选)">
          <input value={form.priority} onChange={(e) => set('priority', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="静态权重(static_weight)">
          <input value={form.staticWeight} onChange={(e) => set('staticWeight', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="并发上限(cap_concurrency)">
          <input value={form.capConcurrency} onChange={(e) => set('capConcurrency', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="标签(逗号分隔)">
          <input value={form.tags} onChange={(e) => set('tags', e.target.value)} placeholder="prod, us, tier1" style={inp} />
        </Field>
        <Field label="变更原因(可选,记入审计)">
          <input value={form.reason} onChange={(e) => set('reason', e.target.value)} style={inp} />
        </Field>
        {error && <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{error}</div>}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} style={ghost}>
            取消
          </button>
          <button type="button" disabled={busy} onClick={submit} style={primary}>
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
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

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const primary: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghost: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
