import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listAuditEvents } from './api'
import { severityTone } from './audit'
import { EMPTY_AUDIT_FILTERS, type AuditEvent, type AuditFilters } from './types'

/*
 * 安全与审计(运维台,P0)。管线第 8 站。GET /admin/v1/audit-events 只读查看器:
 * 多维过滤(事件类/类型/严重度/操作者/时间段)+ 游标分页(加载更多)+ payload 详情展开。
 * 纯只读:不改任何审计记录(审计账本不可变)。
 */
export function AuditPage() {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [total, setTotal] = useState(0)
  const [nextCursor, setNextCursor] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<AuditFilters>(EMPTY_AUDIT_FILTERS)
  const [filters, setFilters] = useState<AuditFilters>(EMPTY_AUDIT_FILTERS)
  const [expanded, setExpanded] = useState<number | null>(null)

  // 首页加载(过滤变更触发):清空累积,从无 cursor 起。
  const loadFirst = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listAuditEvents(filters, undefined, 100, signal)
        .then((resp) => {
          setEvents(resp.items)
          setTotal(resp.total)
          setNextCursor(resp.next_cursor)
        })
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载审计事件失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [filters],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    loadFirst(ctrl.signal)
    return () => ctrl.abort()
  }, [loadFirst])

  const loadMore = async () => {
    if (!nextCursor) return
    setLoading(true)
    setError(null)
    try {
      const resp = await listAuditEvents(filters, nextCursor, 100)
      setEvents((prev) => [...prev, ...resp.items])
      setNextCursor(resp.next_cursor)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载更多失败')
    } finally {
      setLoading(false)
    }
  }

  const setD = <K extends keyof AuditFilters>(k: K, v: AuditFilters[K]) => setDraft((f) => ({ ...f, [k]: v }))

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>安全与审计</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          管线第 8 站 · 防篡改审计账本(只读)。已载 {events.length} / 共 {total} 条。
        </p>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          setExpanded(null)
          setFilters(draft)
        }}
        style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="事件类(event_class)">
          <input value={draft.eventClass} onChange={(e) => setD('eventClass', e.target.value)} placeholder="如 billing" style={inp} />
        </Field>
        <Field label="事件类型(event_type)">
          <input value={draft.eventType} onChange={(e) => setD('eventType', e.target.value)} style={inp} />
        </Field>
        <Field label="严重度">
          <input value={draft.severity} onChange={(e) => setD('severity', e.target.value)} placeholder="info/warn/error" style={inp} />
        </Field>
        <Field label="操作者 ID">
          <input value={draft.actorId} onChange={(e) => setD('actorId', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="起(本地时间)">
          <input type="datetime-local" value={draft.from} onChange={(e) => setD('from', e.target.value)} style={inp} />
        </Field>
        <Field label="止(本地时间)">
          <input type="datetime-local" value={draft.to} onChange={(e) => setD('to', e.target.value)} style={inp} />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
          <button type="submit" style={primaryBtn}>
            查询
          </button>
          <button type="button" onClick={() => { setDraft(EMPTY_AUDIT_FILTERS); setFilters(EMPTY_AUDIT_FILTERS) }} style={ghostBtn}>
            重置
          </button>
        </div>
      </form>

      {error && <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{error}</div>}

      <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }}>
        {loading && events.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : events.length === 0 ? (
          <Empty>没有匹配的审计事件。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['时间', '事件', '严重度', '操作者', '原因', 'Request ID', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {events.map((ev) => (
                  <FragmentRow key={ev.id} ev={ev} expanded={expanded === ev.id} onToggle={() => setExpanded(expanded === ev.id ? null : ev.id)} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {nextCursor && (
        <button type="button" disabled={loading} onClick={loadMore} style={{ ...ghostBtn, alignSelf: 'center', height: 36 }}>
          {loading ? '加载中…' : '加载更多'}
        </button>
      )}
    </div>
  )
}

function FragmentRow({ ev, expanded, onToggle }: { ev: AuditEvent; expanded: boolean; onToggle: () => void }) {
  return (
    <>
      <tr style={{ borderTop: '1px solid var(--hk-line)' }}>
        <td style={tdMono}>{fmt(ev.created_at)}</td>
        <td style={td}>
          <div style={{ display: 'flex', flexDirection: 'column' }}>
            <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{ev.event_type}</span>
            <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{ev.event_class}</span>
          </div>
        </td>
        <td style={td}>
          <StatusBadge tone={severityTone(ev.severity)}>{ev.severity || '—'}</StatusBadge>
        </td>
        <td style={td}>{actorLabel(ev)}</td>
        <td style={{ ...td, maxWidth: 240, color: 'var(--hk-ink-700)' }}>{ev.reason || '—'}</td>
        <td style={tdMono}>{ev.request_id ? short(ev.request_id) : '—'}</td>
        <td style={{ ...td, textAlign: 'right' }}>
          <button type="button" onClick={onToggle} style={linkBtn}>
            {expanded ? '收起' : '详情'}
          </button>
        </td>
      </tr>
      {expanded && (
        <tr style={{ background: 'var(--hk-surface-sunken)' }}>
          <td colSpan={7} style={{ padding: 'var(--hk-space-4)' }}>
            <pre style={{ margin: 0, fontFamily: 'var(--hk-font-mono)', fontSize: 12, color: 'var(--hk-ink-700)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
              {JSON.stringify(detailObject(ev), null, 2)}
            </pre>
          </td>
        </tr>
      )}
    </>
  )
}

function actorLabel(ev: AuditEvent): string {
  if (ev.actor_id == null && !ev.actor_role) return '系统'
  const role = ev.actor_role ? `${ev.actor_role}` : ''
  const id = ev.actor_id != null ? `#${ev.actor_id}` : ''
  return [role, id].filter(Boolean).join(' ') || '—'
}
function detailObject(ev: AuditEvent): Record<string, unknown> {
  return {
    id: ev.id,
    tenant_id: ev.tenant_id,
    ledger_id: ev.ledger_id,
    claim_id: ev.claim_id,
    provider_account_id: ev.provider_account_id,
    pool_group_id: ev.pool_group_id,
    request_id: ev.request_id,
    payload: ev.payload,
  }
}
function short(s: string): string {
  return s.length > 14 ? `${s.slice(0, 8)}…${s.slice(-4)}` : s
}
function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}
function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'top' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
