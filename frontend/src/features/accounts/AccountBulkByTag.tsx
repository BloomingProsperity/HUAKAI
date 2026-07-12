import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { bulkUpdateAccountsByTag } from './api'
import { buildBulkPayload, EMPTY_BULK_FORM, type BulkByTagForm } from './diagnostics'

/*
 * 按标签批量调参工具条(账号列表页)。运营批量启停/改优先级/改静态权重——按标签命中一批账号一次性下发。
 * 表单校验走纯函数 buildBulkPayload(已变异测试);提交前 window.confirm 二次确认(批量改动,影响面大)。
 * 真码:backend/internal/adminhttp/provider_account_bulk_handler.go(POST /admin/v1/provider-accounts/bulk-by-tag)。
 * 注:enabled='false' 会停用整批命中账号 —— 破坏性,故确认弹窗里显式列出将要下发的字段。
 */

export function AccountBulkByTag({ onApplied }: { onApplied: () => void }) {
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState<BulkByTagForm>(EMPTY_BULK_FORM)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)

  function set<K extends keyof BulkByTagForm>(k: K, v: BulkByTagForm[K]) {
    setForm((f) => ({ ...f, [k]: v }))
  }

  async function submit() {
    setError(null)
    setFlash(null)
    const built = buildBulkPayload(form)
    if ('error' in built) {
      setError(built.error)
      return
    }
    const p = built.payload
    // 二次确认:显式列出标签 + 将下发的字段(尤其停用是破坏性的)。
    const changes: string[] = []
    if (p.enabled !== undefined) changes.push(p.enabled ? '启用' : '停用')
    if (p.priority !== undefined) changes.push(`优先级=${p.priority}`)
    if (p.static_weight !== undefined) changes.push(`静态权重=${p.static_weight}`)
    if (!window.confirm(`将对标签「${p.tag}」命中的所有账号批量${changes.join('、')}。此操作影响面较大,确认执行?`)) {
      return
    }
    setBusy(true)
    try {
      const res = await bulkUpdateAccountsByTag(p)
      setFlash(`已更新 ${res.count} 个账号`)
      setForm(EMPTY_BULK_FORM)
      onApplied()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '批量更新失败')
    } finally {
      setBusy(false)
    }
  }

  if (!open) {
    return (
      <div>
        <button type="button" onClick={() => setOpen(true)} style={ghostBtn}>
          按标签批量调参
        </button>
      </div>
    )
  }

  return (
    <div style={panel}>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)', alignItems: 'flex-end' }}>
        <Field label="标签(必填)">
          <input value={form.tag} onChange={(e) => set('tag', e.target.value)} placeholder="如 prod" style={inp} />
        </Field>
        <Field label="启用">
          <select value={form.enabled} onChange={(e) => set('enabled', e.target.value as BulkByTagForm['enabled'])} style={inp}>
            <option value="">不改</option>
            <option value="true">启用</option>
            <option value="false">停用</option>
          </select>
        </Field>
        <Field label="优先级">
          <input value={form.priority} inputMode="numeric" placeholder="不改则留空" onChange={(e) => set('priority', e.target.value)} style={{ ...inp, width: 110 }} />
        </Field>
        <Field label="静态权重">
          <input value={form.staticWeight} inputMode="numeric" placeholder="不改则留空" onChange={(e) => set('staticWeight', e.target.value)} style={{ ...inp, width: 110 }} />
        </Field>
        <button type="button" onClick={submit} disabled={busy} style={primaryBtn}>
          {busy ? '执行中…' : '批量应用'}
        </button>
        <button type="button" onClick={() => { setOpen(false); setError(null); setFlash(null) }} style={ghostBtn}>
          收起
        </button>
      </div>
      {error && <p style={{ color: 'var(--hk-danger, var(--hk-danger))', margin: 0, fontSize: 13 }}>{error}</p>}
      {flash && <p style={{ color: 'var(--hk-ok, var(--hk-success))', margin: 0, fontSize: 13 }}>{flash}</p>}
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

const panel: React.CSSProperties = {
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  padding: 'var(--hk-space-4)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-2)',
}
const inp: React.CSSProperties = {
  height: 32,
  minWidth: 120,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
}
const primaryBtn: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-4)',
  border: '1px solid var(--hk-primary-600)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-primary-500)',
  color: '#fff',
  fontSize: 13,
  fontWeight: 600,
  cursor: 'pointer',
}
const ghostBtn: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-4)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-700)',
  fontSize: 13,
  cursor: 'pointer',
}
