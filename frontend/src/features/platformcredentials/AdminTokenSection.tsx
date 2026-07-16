import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { createAdminToken, listAdminTokens, revokeAdminToken } from './api'
import {
  buildAdminTokenRequest,
  credentialStatusLabel,
  credentialStatusTone,
  EMPTY_ADMIN_TOKEN_FORM,
  mapAdminTokenTableRows,
  type AdminTokenForm,
  type AdminTokenTableRow,
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
  const tableRows = mapAdminTokenTableRows(rows)

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
          <EmptyState title="正在加载运维令牌" hint="请稍候。" />
        ) : rows.length === 0 ? (
          <EmptyState title="暂无运维令牌" hint="签发后仅在创建响应中展示一次明文。" />
        ) : (
          <DataListTable
            label="运维令牌列表"
            rows={tableRows}
            rowKey={(row) => row.id}
            columns={adminTokenColumns}
            actions={[{
              label: (row) => revokeID === row.id ? '吊销中…' : '吊销',
              tone: 'danger',
              disabled: (row) => !row.revocable || revokeID === row.id,
              onClick: (row) => revoke(row.source),
            }]}
          />
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

const adminTokenColumns: DataListColumn<AdminTokenTableRow>[] = [
  { key: 'name', label: '名称 / 前缀', render: (row) => <StackedCredential primary={row.name} secondary={row.keyPrefix} strong /> },
  { key: 'role', label: '角色 / 作用域', render: (row) => <StackedCredential primary={row.role} secondary={row.scope} /> },
  { key: 'status', label: '状态', render: (row) => <><StatusBadge tone={credentialStatusTone(row.status)}>{credentialStatusLabel(row.status)}</StatusBadge>{row.bootstrap && <div style={subtle}>启动令牌</div>}</> },
  { key: 'expires', label: '过期时间', render: (row) => <span className="hk-mono">{row.expiresAt}</span> },
  { key: 'last-used', label: '最近使用', render: (row) => <span className="hk-mono">{row.lastUsedAt}</span> },
  { key: 'created', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
]

function StackedCredential({ primary, secondary, strong = false }: { primary: string; secondary: string; strong?: boolean }) {
  return <span style={{ display: 'flex', flexDirection: 'column' }}>{strong ? <strong>{primary}</strong> : <span>{primary}</span>}<span className="hk-mono">{secondary}</span></span>
}
