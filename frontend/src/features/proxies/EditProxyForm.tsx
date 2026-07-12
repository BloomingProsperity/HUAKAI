import { useState } from 'react'
import { updateProxy } from './api'
import { buildUpdateInput, PROTOCOLS, validateEditForm, type EditProxyForm as EditForm } from './proxies'
import type { Proxy } from './types'

/*
 * 编辑出口代理(PATCH /admin/v1/proxies/{id})。改 name/protocol/host/port/认证。
 * 校验走纯函数 validateEditForm + buildUpdateInput(已变异测试)。
 * ⚠️ auth_secret:后端 PATCH 不区分「字段缺省」与「显式置空」——缺省即写 SQL NULL
 *   (admin_proxies UPDATE 无条件 auth_secret=$N,无 COALESCE),且密钥从不回显无法 round-trip。
 *   故「留空保存」会清除该代理已存的认证密钥,而非保留。本表单如实标注并在留空保存前二次确认,
 *   不再承诺「留空=不改」。要保留认证就重新填密钥。状态切换走列表行内 PUT /{id}/status。
 */

export function EditProxyForm({
  tenantId,
  proxy,
  onSaved,
  onCancel,
}: {
  tenantId: number
  proxy: Proxy
  onSaved: () => void
  onCancel: () => void
}) {
  // auth_secret 永远从空开始(后端从不回显密钥);auth_username 用已知值预填。
  const [form, setForm] = useState<EditForm>({
    name: proxy.name,
    protocol: proxy.protocol,
    host: proxy.host,
    port: String(proxy.port),
    auth_username: proxy.auth_username ?? '',
    auth_secret: '',
  })
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function set<K extends keyof EditForm>(k: K, v: string) {
    setForm((f) => ({ ...f, [k]: v }))
  }

  async function submit() {
    const invalid = validateEditForm(form)
    if (invalid) {
      setError(invalid)
      return
    }
    // ⚠️ 留空 = 清除密钥(后端缺省即写 NULL)。留空保存前显式二次确认,避免静默抹掉认证。
    if (form.auth_secret.trim() === '') {
      const hadAuth = (proxy.auth_username ?? '').trim() !== ''
      const warn = hadAuth
        ? `认证密钥留空,保存将清除该代理已配置的认证密钥(用户名「${proxy.auth_username}」将失去配套密钥,可能导致出站认证失败)。确定继续?`
        : '认证密钥留空,保存后该代理将无认证密钥。确定继续?'
      if (!window.confirm(warn)) return
    }
    setSubmitting(true)
    setError(null)
    try {
      await updateProxy(tenantId, proxy.id, buildUpdateInput(form))
      onSaved()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div style={panel}>
      <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <Field label="名称"><input value={form.name} onChange={(e) => set('name', e.target.value)} style={inp} /></Field>
        <Field label="协议">
          <select value={form.protocol} onChange={(e) => set('protocol', e.target.value)} style={inp}>
            {PROTOCOLS.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
        </Field>
        <Field label="主机"><input value={form.host} onChange={(e) => set('host', e.target.value)} style={inp} /></Field>
        <Field label="端口"><input value={form.port} inputMode="numeric" onChange={(e) => set('port', e.target.value)} style={{ ...inp, width: 90 }} /></Field>
      </div>
      <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <Field label="认证用户名(留空清除)"><input value={form.auth_username} autoComplete="off" onChange={(e) => set('auth_username', e.target.value)} style={inp} /></Field>
        <Field label="认证密钥(留空将清除认证!)"><input type="password" value={form.auth_secret} autoComplete="off" placeholder="留空=清除;要保留认证请重填" onChange={(e) => set('auth_secret', e.target.value)} style={inp} /></Field>
      </div>
      {error && <p style={{ color: 'var(--hk-danger, var(--hk-danger))', margin: 0, fontSize: 13 }}>{error}</p>}
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
        <button type="button" onClick={submit} disabled={submitting} style={primaryBtn}>{submitting ? '保存中…' : '保存'}</button>
        <button type="button" onClick={onCancel} style={ghostBtn}>取消</button>
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

const panel: React.CSSProperties = {
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-3)',
  padding: 'var(--hk-space-4)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
  background: 'var(--hk-surface, #fff)',
}
const inp: React.CSSProperties = { padding: '6px 8px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-2)', fontSize: 13 }
const primaryBtn: React.CSSProperties = { padding: '7px 14px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-2)', background: 'var(--hk-accent, #2563eb)', color: '#fff', fontSize: 13, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { padding: '7px 14px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-2)', background: 'transparent', fontSize: 13, cursor: 'pointer' }
