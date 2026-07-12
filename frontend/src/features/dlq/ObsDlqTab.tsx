import { Fragment, useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listObsDlq, replayObsDlq } from './api'
import {
  formatPayload,
  formatTs,
  LIMIT_DEFAULT,
  obsPriorityTone,
  obsStatusLabel,
  obsStatusTone,
  shortReason,
  validateLimit,
} from './dlq'
import type { ObsDlqRecord } from './types'

interface ObsQuery {
  tenantId?: number
  eventType?: string
  limit: number
}

export function ObsDlqTab() {
  const [tenantInput, setTenantInput] = useState('')
  const [eventType, setEventType] = useState('')
  const [limitInput, setLimitInput] = useState(String(LIMIT_DEFAULT))
  const [query, setQuery] = useState<ObsQuery>({ limit: LIMIT_DEFAULT })
  const [rows, setRows] = useState<ObsDlqRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [replayingID, setReplayingID] = useState<string | null>(null)
  const [expandedID, setExpandedID] = useState<string | null>(null)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listObsDlq({ ...query, signal })
        .then((response) => setRows(response.items ?? []))
        .catch((cause: unknown) => {
          if (signal?.aborted) return
          setError(cause instanceof ApiError ? `${cause.message}(${cause.code})` : '加载观测死信失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [query],
  )

  useEffect(() => {
    const controller = new AbortController()
    load(controller.signal)
    return () => controller.abort()
  }, [load])

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const limit = validateLimit(Number(limitInput.trim()))
    if (!limit.ok) {
      setError(limit.error)
      return
    }
    const rawTenant = tenantInput.trim()
    const tenantID = rawTenant === '' ? undefined : Number(rawTenant)
    if (tenantID !== undefined && (!Number.isSafeInteger(tenantID) || tenantID <= 0)) {
      setError('租户 ID 必须是正整数')
      return
    }
    setNotice(null)
    setExpandedID(null)
    setQuery({ tenantId: tenantID, eventType: eventType.trim() || undefined, limit: limit.value })
  }

  const replay = async (record: ObsDlqRecord) => {
    if (!window.confirm(`确认重放观测死信 ${record.id}（${record.event_type}）？事件将重新进入观测 outbox。`)) {
      return
    }
    setReplayingID(record.id)
    setError(null)
    setNotice(null)
    try {
      const response = await replayObsDlq(record.id)
      setNotice(`观测死信 ${response.id} 已重新进入 outbox（事件 ${response.outbox_event_id}）。`)
      load()
    } catch (cause) {
      setError(cause instanceof ApiError ? `重放失败：${cause.message}(${cause.code})` : '重放观测死信失败')
    } finally {
      setReplayingID(null)
    }
  }

  return (
    <div className="hk-col">
      <form onSubmit={submit} className="hk-card" style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', flexWrap: 'wrap', padding: 'var(--hk-space-4)' }}>
        <Field label="租户 ID（可选）">
          <input value={tenantInput} onChange={(event) => setTenantInput(event.target.value)} inputMode="numeric" style={{ ...inputStyle, width: 150 }} />
        </Field>
        <Field label="事件类型（可选）">
          <input value={eventType} onChange={(event) => setEventType(event.target.value)} placeholder="如 email.retry" style={{ ...inputStyle, width: 220 }} />
        </Field>
        <Field label="条数（1~200）">
          <input value={limitInput} onChange={(event) => setLimitInput(event.target.value)} inputMode="numeric" style={{ ...inputStyle, width: 120 }} />
        </Field>
        <button type="submit" disabled={loading} className="hk-btn hk-btn--green">
          {loading ? '查询中…' : '查询'}
        </button>
      </form>

      {error && <Banner tone="danger">{error}</Banner>}
      {notice && <Banner tone="ok">{notice}</Banner>}

      <section className="hk-card">
        <div className="hk-card__head">
          <h3>观测死信</h3>
          <span style={{ marginLeft: 'auto', color: 'var(--hk-ink-300)', fontSize: 11 }}>
            {loading ? '刷新中…' : `共 ${rows.length} 条`}
          </span>
        </div>
        <ObsDlqTable
          rows={rows}
          loading={loading}
          replayingID={replayingID}
          expandedID={expandedID}
          onToggle={(id) => setExpandedID((current) => (current === id ? null : id))}
          onReplay={replay}
        />
      </section>
    </div>
  )
}

export function ObsDlqTable({
  rows,
  loading,
  replayingID,
  expandedID,
  onToggle,
  onReplay,
}: {
  rows: ObsDlqRecord[]
  loading: boolean
  replayingID: string | null
  expandedID: string | null
  onToggle: (id: string) => void
  onReplay: (record: ObsDlqRecord) => void
}) {
  if (loading && rows.length === 0) return <div className="hk-empty">加载中…</div>
  if (rows.length === 0) return <div className="hk-empty">当前条件下暂无观测死信。</div>

  return (
    <div className="hk-tablewrap">
      <table className="hk-table">
        <thead>
          <tr>
            {['死信 ID', '租户', '事件类型', '优先级', '状态', '尝试', '失败原因', '死信时间', ''].map((title) => (
              <th key={title}>{title}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((record) => {
            const expanded = expandedID === record.id
            return (
              <Fragment key={record.id}>
                <tr>
                  <td className="hk-mono">
                    <button type="button" onClick={() => onToggle(record.id)} style={linkButton} title="展开或收起详情">
                      {record.id} {expanded ? '▾' : '▸'}
                    </button>
                  </td>
                  <td className="hk-mono">#{record.tenant_id}</td>
                  <td className="hk-mono">{record.event_type}</td>
                  <td>
                    <StatusBadge tone={obsPriorityTone(record.priority)}>{record.priority || 'default'}</StatusBadge>
                  </td>
                  <td>
                    <StatusBadge tone={obsStatusTone(record.outbox_status)}>{obsStatusLabel(record.outbox_status)}</StatusBadge>
                  </td>
                  <td className="hk-mono">{record.attempt_count}</td>
                  <td style={{ maxWidth: 280 }}>{shortReason(record.dead_reason || record.failure_reason)}</td>
                  <td className="hk-mono">{formatTs(record.dead_at)}</td>
                  <td style={{ textAlign: 'right' }}>
                    <button
                      type="button"
                      className="hk-btn hk-btn--green hk-btn--sm"
                      disabled={replayingID === record.id}
                      onClick={() => onReplay(record)}
                    >
                      {replayingID === record.id ? '重放中…' : '重放'}
                    </button>
                  </td>
                </tr>
                {expanded && (
                  <tr style={{ background: 'var(--hk-surface-sunken)' }}>
                    <td colSpan={9} style={{ padding: 'var(--hk-space-4)' }}>
                      <div className="hk-kv" style={{ marginBottom: 'var(--hk-space-3)' }}>
                        <KV label="outbox_event_id" value={record.outbox_event_id} />
                        <KV label="failure_reason" value={record.failure_reason || '—'} />
                        <KV label="created_at" value={formatTs(record.created_at)} />
                        <KV label="next_retry_at" value={formatTs(record.next_retry_at)} />
                      </div>
                      <details>
                        <summary style={{ cursor: 'pointer', color: 'var(--hk-ink-500)', fontSize: 12 }}>payload（原始载荷）</summary>
                        <pre style={payloadStyle}>{formatPayload(record.payload)}</pre>
                      </details>
                    </td>
                  </tr>
                )}
              </Fragment>
            )
          })}
        </tbody>
      </table>
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
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', color: danger ? 'var(--hk-danger)' : 'var(--hk-primary-700)', background: danger ? 'var(--hk-danger-soft)' : 'var(--hk-primary-50)', fontSize: 13 }}>{children}</div>
}

function KV({ label, value }: { label: string; value: string }) {
  return (
    <div className="hk-kv__r">
      <span className="hk-kv__k">{label}</span>
      <span className="hk-kv__v hk-mono">{value}</span>
    </div>
  )
}

const inputStyle: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontSize: 13 }
const linkButton: React.CSSProperties = { border: 0, background: 'transparent', color: 'var(--hk-primary-600)', cursor: 'pointer', padding: 0, fontFamily: 'var(--hk-font-mono)' }
const payloadStyle: React.CSSProperties = { margin: 'var(--hk-space-2) 0 0', padding: 'var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontFamily: 'var(--hk-font-mono)', fontSize: 12, maxHeight: 320, overflow: 'auto' }
