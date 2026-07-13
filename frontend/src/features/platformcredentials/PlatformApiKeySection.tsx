import { useCallback, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { createPlatformApiKey, listPlatformApiKeys, revokePlatformApiKey } from './api'
import {
  buildPlatformApiKeyRequest,
  credentialStatusLabel,
  credentialStatusTone,
  EMPTY_PLATFORM_API_KEY_FORM,
  mapPlatformApiKeyTableRows,
  positiveID,
  type PlatformApiKeyForm,
  type PlatformApiKeyTableRow,
} from './credentials'
import { OneTimeSecretBox } from './OneTimeSecretBox'
import type { CreatedPlatformApiKey, PlatformApiKeyEnvironment, PlatformApiKeyListItem } from './types'

export function PlatformApiKeySection() {
  const [rows, setRows] = useState<PlatformApiKeyListItem[]>([])
  const [tenantFilter, setTenantFilter] = useState('')
  const [activeTenantID, setActiveTenantID] = useState<number | null>(null)
  const [form, setForm] = useState<PlatformApiKeyForm>(EMPTY_PLATFORM_API_KEY_FORM)
  const [created, setCreated] = useState<CreatedPlatformApiKey | null>(null)
  const [loading, setLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [revokeID, setRevokeID] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const load = useCallback((tenantID: number, signal?: AbortSignal) => {
    setLoading(true)
    setError(null)
    listPlatformApiKeys(tenantID, 100, 0, signal)
      .then((response) => setRows(response.items ?? []))
      .catch((cause: unknown) => {
        if (signal?.aborted) return
        setError(apiMessage(cause, '加载平台 API Key 失败'))
      })
      .finally(() => {
        if (!signal?.aborted) setLoading(false)
      })
  }, [])

  const query = (event: React.FormEvent) => {
    event.preventDefault()
    const tenantID = positiveID(tenantFilter)
    if (tenantID === null) {
      setError('请填写有效租户 ID 后查询')
      return
    }
    setActiveTenantID(tenantID)
    setNotice(null)
    load(tenantID)
  }

  const set = <K extends keyof PlatformApiKeyForm>(key: K, value: PlatformApiKeyForm[K]) =>
    setForm((current) => ({ ...current, [key]: value }))

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    const built = buildPlatformApiKeyRequest(form)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setCreated(null)
    setSubmitting(true)
    setError(null)
    setNotice(null)
    try {
      const response = await createPlatformApiKey(built.value)
      setCreated(response)
      setTenantFilter(String(response.tenant_id))
      setActiveTenantID(response.tenant_id)
      setForm({ ...EMPTY_PLATFORM_API_KEY_FORM, tenantId: String(response.tenant_id) })
      load(response.tenant_id)
    } catch (cause) {
      setError(apiMessage(cause, '签发平台 API Key 失败'))
    } finally {
      setSubmitting(false)
    }
  }

  const revoke = async (item: PlatformApiKeyListItem) => {
    if (!window.confirm(`确认吊销平台 API Key「${item.name}」(#${item.id})？吊销后不可恢复。`)) return
    setRevokeID(item.id)
    setError(null)
    setNotice(null)
    try {
      const response = await revokePlatformApiKey(item.id, item.tenant_id, '运营台手动吊销')
      setNotice(response.already_revoked ? `Key #${item.id} 此前已吊销。` : `Key #${item.id} 已吊销。`)
      load(item.tenant_id)
    } catch (cause) {
      setError(apiMessage(cause, '吊销平台 API Key 失败'))
    } finally {
      setRevokeID(null)
    }
  }
  const tableRows = mapPlatformApiKeyTableRows(rows)

  return (
    <div className="hk-col">
      <form className="hk-card" onSubmit={submit}>
        <div className="hk-card__head">
          <h3>签发平台级 API Key</h3>
          <span style={headHint}>明文仅在创建响应中返回一次</span>
        </div>
        <div className="hk-card__body" style={formGrid}>
          <Field label="租户 ID">
            <input value={form.tenantId} onChange={(event) => set('tenantId', event.target.value)} inputMode="numeric" style={inputStyle} />
          </Field>
          <Field label="用户 ID">
            <input value={form.userId} onChange={(event) => set('userId', event.target.value)} inputMode="numeric" style={inputStyle} />
          </Field>
          <Field label="名称">
            <input value={form.name} onChange={(event) => set('name', event.target.value)} maxLength={128} style={inputStyle} />
          </Field>
          <Field label="环境">
            <select
              value={form.environment}
              onChange={(event) => set('environment', event.target.value as PlatformApiKeyEnvironment)}
              style={inputStyle}
            >
              <option value="live">live（生产）</option>
              <option value="test">test（测试）</option>
            </select>
          </Field>
          <Field label="过期时间（留空为永久）">
            <input type="datetime-local" value={form.expiresAt} onChange={(event) => set('expiresAt', event.target.value)} style={inputStyle} />
          </Field>
          <Field label="签发原因（可选）">
            <input value={form.reason} onChange={(event) => set('reason', event.target.value)} maxLength={500} style={inputStyle} />
          </Field>
          <div style={{ alignSelf: 'end' }}>
            <button type="submit" className="hk-btn hk-btn--green" disabled={submitting}>
              {submitting ? '签发中…' : '签发 API Key'}
            </button>
          </div>
        </div>
        {created && (
          <OneTimeSecretBox
            kind="平台 API Key"
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
          <h3>平台 API Key 列表</h3>
          <span style={headHint}>{activeTenantID ? `租户 #${activeTenantID} · ${rows.length} 条` : '须先指定租户'}</span>
        </div>
        <form onSubmit={query} style={{ display: 'flex', alignItems: 'flex-end', gap: 'var(--hk-space-2)', padding: 'var(--hk-space-3) var(--hk-space-4)', borderBottom: '1px solid var(--hk-line-soft)' }}>
          <Field label="租户 ID">
            <input value={tenantFilter} onChange={(event) => setTenantFilter(event.target.value)} inputMode="numeric" style={{ ...inputStyle, width: 180 }} />
          </Field>
          <button type="submit" className="hk-btn hk-btn--green" disabled={loading}>
            {loading ? '查询中…' : '查询'}
          </button>
        </form>
        {activeTenantID === null ? (
          <EmptyState title="须先指定租户" hint="平台列表按租户隔离，请输入租户 ID 查询。" />
        ) : loading && rows.length === 0 ? (
          <EmptyState title="正在加载平台 API Key" hint="请稍候。" />
        ) : rows.length === 0 ? (
          <EmptyState title="该租户暂无平台 API Key" hint="签发后列表仅展示名称与脱敏前缀。" />
        ) : (
          <DataListTable
            label="平台 API Key 列表"
            rows={tableRows}
            rowKey={(row) => row.id}
            columns={platformApiKeyColumns}
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

const platformApiKeyColumns: DataListColumn<PlatformApiKeyTableRow>[] = [
  { key: 'name', label: '名称 / 前缀', render: (row) => <span style={{ display: 'flex', flexDirection: 'column' }}><strong>{row.name}</strong><span className="hk-mono">{row.keyPrefix}</span></span> },
  { key: 'user', label: '用户', render: (row) => <span className="hk-mono">{row.userID}</span> },
  { key: 'status', label: '状态', render: (row) => <StatusBadge tone={credentialStatusTone(row.status)}>{credentialStatusLabel(row.status)}</StatusBadge> },
  { key: 'expires', label: '过期时间', render: (row) => <span className="hk-mono">{row.expiresAt}</span> },
  { key: 'last-used', label: '最近使用', render: (row) => <span className="hk-mono">{row.lastUsedAt}</span> },
  { key: 'created', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
]
