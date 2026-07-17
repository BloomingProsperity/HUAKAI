import { useState } from 'react'
import { createProxy } from './api'
import { buildCreateInput, PROTOCOLS, STATUSES, validateCreateForm, type CreateProxyForm } from './proxies'

/*
 * 新建出口代理表单。校验走纯函数 validateCreateForm(已变异测试);提交成功后回调刷新列表。
 * auth_secret 用 password 框,仅创建时随请求发出(后端加密存、从不回显)。
 */

const empty: CreateProxyForm = { name: '', protocol: 'http', host: '', port: '', auth_username: '', auth_secret: '', group_id: '', status: 'active' }

export function ProxyCreateForm({ tenantId, onCreated }: { tenantId: number; onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState<CreateProxyForm>(empty)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function set<K extends keyof CreateProxyForm>(k: K, v: string) {
    setForm((f) => ({ ...f, [k]: v }))
  }

  async function submit() {
    const invalid = validateCreateForm(form)
    if (invalid) {
      setError(invalid)
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      await createProxy(tenantId, buildCreateInput(form))
      setForm(empty)
      setOpen(false)
      onCreated()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : '创建失败')
    } finally {
      setSubmitting(false)
    }
  }

  if (!open) {
    return (
      <div>
        <button type="button" onClick={() => setOpen(true)} style={primaryBtn}>+ 新建代理</button>
      </div>
    )
  }

  return (
    <div style={{ border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-3)', padding: 'var(--hk-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <Field label="名称"><input value={form.name} onChange={(e) => set('name', e.target.value)} style={inp} /></Field>
        <Field label="协议">
          <select value={form.protocol} onChange={(e) => set('protocol', e.target.value)} style={inp}>
            {PROTOCOLS.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
        </Field>
        <Field label="主机"><input value={form.host} placeholder="如 1.2.3.4 或 proxy.example.com" onChange={(e) => set('host', e.target.value)} style={inp} /></Field>
        <Field label="端口"><input value={form.port} inputMode="numeric" onChange={(e) => set('port', e.target.value)} style={{ ...inp, width: 90 }} /></Field>
        <Field label="代理组(可选)">
          <input name="group_id" value={form.group_id} maxLength={64} pattern="[A-Za-z0-9_-]{0,64}" placeholder="如 us-residential" onChange={(e) => set('group_id', e.target.value)} style={inp} />
          <span style={fieldHint}>仅限字母、数字、下划线、短横线，最长 64；留空表示未分组。</span>
        </Field>
        <Field label="状态">
          <select value={form.status} onChange={(e) => set('status', e.target.value)} style={inp}>
            {STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </Field>
      </div>
      <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <Field label="认证用户名(可选)"><input value={form.auth_username} autoComplete="off" onChange={(e) => set('auth_username', e.target.value)} style={inp} /></Field>
        <Field label="认证密钥(可选)"><input type="password" value={form.auth_secret} autoComplete="off" onChange={(e) => set('auth_secret', e.target.value)} style={inp} /></Field>
      </div>
      {error && <p style={{ color: 'var(--hk-danger, var(--hk-danger))', margin: 0, fontSize: 13 }}>{error}</p>}
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
        <button type="button" onClick={submit} disabled={submitting} style={primaryBtn}>{submitting ? '创建中…' : '创建'}</button>
        <button type="button" onClick={() => { setOpen(false); setError(null) }} style={ghostBtn}>取消</button>
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

const inp: React.CSSProperties = { padding: '6px 8px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-2)', fontSize: 13 }
const fieldHint: React.CSSProperties = { color: 'var(--hk-ink-300)', fontSize: 11, lineHeight: 1.4 }
const primaryBtn: React.CSSProperties = { padding: '7px 14px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-2)', background: 'var(--hk-accent, #2563eb)', color: '#fff', fontSize: 13, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { padding: '7px 14px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-2)', background: 'transparent', fontSize: 13, cursor: 'pointer' }
