import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { createAdminToken, listAdminTokens, revokeAdminToken } from './api'
import {
  buildAdminTokenRequest,
  credentialStatusLabel,
  credentialStatusTone,
  EMPTY_ADMIN_TOKEN_FORM,
  formatCredentialTime,
  type AdminTokenForm,
} from './credentials'
import { OneTimeSecretBox } from './OneTimeSecretBox'
import type { AdminTokenListItem, AdminTokenRole, CreatedAdminToken } from './types'

export function AdminTokenSection() {
  const [rows, setRows] = useState<AdminTokenListItem[]>([])
  const [form, setForm] = useState<AdminTokenForm>(EMPTY_ADMIN_TOKEN_FORM)
  const [created, setCreated] = useState<CreatedAdminToken | null>(null)
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [revokeID, setRevokeID] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const load = useCallback((signal?: AbortSignal) => {
    setLoading(true)
    setError(null)
    listAdminTokens(100, 0, signal)
      .then((response) => setRows(response.items ?? []))
      .catch((cause: unknown) => {
        if (signal?.aborted) return
        setError(apiMessage(cause, '加载运维令牌失败'))
      })
      .finally(() => {
        if (!signal?.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    load(controller.signal)
    return () => controller.abort()
  }, [load])

  const set = <K extends keyof AdminTokenForm>(key: K, value: AdminTokenForm[K]) =>
    setForm((current) => ({ ...current, [key]: value }))

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    const built = buildAdminTokenRequest(form)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setCreated(null)
    setSubmitting(true)
    setError(null)
    setNotice(null)
    try {
      const response = await createAdminToken(built.value)
      setCreated(response)
      setForm(EMPTY_ADMIN_TOKEN_FORM)
      load()
    } catch (cause) {
      setError(apiMessage(cause, '签发运维令牌失败'))
    } finally {
      setSubmitting(false)
    }
  }

  const revoke = async (item: AdminTokenListItem) => {
    if (!window.confirm(`确认吊销运维令牌「${item.name || item.key_prefix}」(#${item.id})？吊销后不可恢复。`)) {
      return
    }
    setRevokeID(item.id)
    setError(null)
    setNotice(null)
    try {
      const response = await revokeAdminToken(item.id, '运营台手动吊销')
      setNotice(response.already_revoked ? `令牌 #${item.id} 此前已吊销。` : `令牌 #${item.id} 已吊销。`)
      load()
    } catch (cause) {
      setError(apiMessage(cause, '吊销运维令牌失败'))
    } finally {
      setRevokeID(null)
    }
  }

  return (
    <div className="hk-col">
      <form className="hk-card" onSubmit={submit}>
        <div className="hk-card__head">
          <h3>签发运维令牌</h3>
          <span style={headHint}>明文仅在创建响应中返回一次</span>
        </div>
        <div className="hk-card__body" style={formGrid}>
          <Field label="名称（可选）">
            <input value={form.name} onChange={(event) => set('name', event.target.value)} style={inputStyle} maxLength={128} />
          </Field>
          <Field label="角色">
            <select
              value={form.role}
              onChange={(event) => set('role', event.target.value as AdminTokenRole)}
              style={inputStyle}
            >
              <option value="platform_admin">平台管理员</option>
              <option value="tenant_operator">租户运维</option>
            </select>
          </Field>
          {form.role === 'tenant_operator' && (
            <Field label="租户 ID">
              <input value={form.tenantId} onChange={(event) => set('tenantId', event.target.value)} inputMode="numeric" style={inputStyle} />
            </Field>
          )}
          <Field label="过期时间（留空为永久）">
            <input type="datetime-local" value={form.expiresAt} onChange={(event) => set('expiresAt', event.target.value)} style={inputStyle} />
          </Field>
          <Field label="备注（可选）">
            <input value={form.note} onChange={(event) => set('note', event.target.value)} style={inputStyle} maxLength={500} />
          </Field>
          <div style={{ alignSelf: 'end' }}>
            <button type="submit" className="hk-btn hk-btn--green" disabled={submitting}>
              {submitting ? '签发中…' : '签发令牌'}
            </button>
          </div>
        </div>
        {created && (
          <OneTimeSecretBox
            kind="运维令牌"
            plaintext={created.plaintext_bearer}
            keyPrefix={created.key_prefix}
            onClose={() => setCreated(null)}
          />
        )}
      </form>

      {error && <Banner tone="danger">{error}</Banner>}
      {notice && <Banner tone="ok">{notice}</Banner>}

      <section className="hk-card">
        <div className="hk-card__head">
          <h3>运维令牌列表</h3>
          <span style={headHint}>{loading ? '刷新中…' : `共 ${rows.length} 条`}</span>
        </div>
        {loading && rows.length === 0 ? (
          <div className="hk-empty">加载中…</div>
        ) : rows.length === 0 ? (
          <div className="hk-empty">暂无运维令牌。</div>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['名称 / 前缀', '角色 / 作用域', '状态', '过期时间', '最近使用', '创建时间', ''].map((title) => (
                    <th key={title}>{title}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((item) => (
                  <tr key={item.id}>
                    <td>
                      <strong>{item.name || `令牌 #${item.id}`}</strong>
                      <div className="hk-mono">{item.key_prefix}</div>
                    </td>
                    <td>
                      {item.role === 'platform_admin' ? '平台管理员' : '租户运维'}
                      <div className="hk-mono">{item.scope_tenant_id ? `租户 #${item.scope_tenant_id}` : '全平台'}</div>
                    </td>
                    <td>
                      <StatusBadge tone={credentialStatusTone(item.status)}>{credentialStatusLabel(item.status)}</StatusBadge>
                      {item.bootstrap && <div style={subtle}>启动令牌</div>}
                    </td>
                    <td className="hk-mono">{formatCredentialTime(item.expires_at)}</td>
                    <td className="hk-mono">{item.last_used_at ? formatCredentialTime(item.last_used_at) : '从未使用'}</td>
                    <td className="hk-mono">{formatCredentialTime(item.created_at)}</td>
                    <td style={{ textAlign: 'right' }}>
                      <button
                        type="button"
                        className="hk-btn hk-btn--danger hk-btn--sm"
                        disabled={item.status !== 'active' || item.revoked_at !== null || revokeID === item.id}
                        onClick={() => revoke(item)}
                      >
                        {revokeID === item.id ? '吊销中…' : '吊销'}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, color: 'var(--hk-ink-500)', fontSize: 12 }}>
      {label}
      {children}
    </label>
  )
}

function Banner({ tone, children }: { tone: 'danger' | 'ok'; children: React.ReactNode }) {
  const danger = tone === 'danger'
  return (
    <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', color: danger ? 'var(--hk-danger)' : 'var(--hk-primary-700)', background: danger ? 'var(--hk-danger-soft)' : 'var(--hk-primary-50)', fontSize: 13 }}>
      {children}
    </div>
  )
}

function apiMessage(cause: unknown, fallback: string): string {
  return cause instanceof ApiError ? `${cause.message}(${cause.code})` : fallback
}

const inputStyle: React.CSSProperties = { width: '100%', height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontSize: 13 }
const formGrid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 'var(--hk-space-3)', alignItems: 'end' }
const headHint: React.CSSProperties = { marginLeft: 'auto', color: 'var(--hk-ink-300)', fontSize: 11 }
const subtle: React.CSSProperties = { marginTop: 3, color: 'var(--hk-ink-300)', fontSize: 10 }
