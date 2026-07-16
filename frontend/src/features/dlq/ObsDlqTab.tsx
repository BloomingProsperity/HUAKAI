import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { listObsDlq, replayObsDlq } from './api'
import {
  formatPayload,
  formatTs,
  LIMIT_DEFAULT,
  mapObsDlqRows,
  validateLimit,
  type ObsDlqTableRow,
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
  if (loading && rows.length === 0) return <EmptyState title="正在加载观测死信" hint="请稍候。" />
  if (rows.length === 0) return <EmptyState title="暂无观测死信" hint="当前查询条件下没有需要处理的观测死信。" />

  const tableRows = mapObsDlqRows(rows)
  const expanded = expandedID == null ? undefined : rows.find((record) => record.id === expandedID)

  return (
    <>
      <DataListTable
        label="观测死信"
        rows={tableRows}
        rowKey={(row) => row.id}
        columns={obsDlqColumns(expandedID, onToggle)}
        actions={[{
          label: (row) => replayingID === row.id ? '重放中…' : '重放',
          disabled: (row) => replayingID === row.id,
          onClick: (row) => onReplay(row.record),
        }]}
      />
      {expanded && (
        <div style={{ padding: 'var(--hk-space-4)', background: 'var(--hk-surface-sunken)', borderTop: '1px solid var(--hk-line)' }}>
          <div className="hk-kv" style={{ marginBottom: 'var(--hk-space-3)' }}>
            <KV label="outbox_event_id" value={expanded.outbox_event_id} />
            <KV label="failure_reason" value={expanded.failure_reason || '—'} />
            <KV label="created_at" value={formatTs(expanded.created_at)} />
            <KV label="next_retry_at" value={formatTs(expanded.next_retry_at)} />
          </div>
          <details>
            <summary style={{ cursor: 'pointer', color: 'var(--hk-ink-500)', fontSize: 12 }}>payload（原始载荷）</summary>
            <pre style={payloadStyle}>{formatPayload(expanded.payload)}</pre>
          </details>
        </div>
      )}
    </>
  )
}

function obsDlqColumns(expandedID: string | null, onToggle: (id: string) => void): DataListColumn<ObsDlqTableRow>[] {
  return [
    { key: 'id', label: '死信 ID', render: (row) => <button type="button" onClick={() => onToggle(row.id)} style={linkButton} title="展开或收起详情">{row.id} {expandedID === row.id ? '▾' : '▸'}</button> },
    { key: 'tenant', label: '租户', render: (row) => <span className="hk-mono">{row.tenant}</span> },
    { key: 'event-type', label: '事件类型', render: (row) => <span className="hk-mono">{row.eventType}</span> },
    { key: 'priority', label: '优先级', badge: true, render: (row) => <StatusBadge tone={row.priorityTone}>{row.priority}</StatusBadge> },
    { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
    { key: 'attempts', label: '尝试', render: (row) => <span className="hk-mono">{row.attempts}</span> },
    { key: 'reason', label: '失败原因', render: (row) => <span style={{ maxWidth: 280 }}>{row.reason}</span> },
    { key: 'dead-at', label: '死信时间', render: (row) => <span className="hk-mono">{row.deadAt}</span> },
  ]
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
