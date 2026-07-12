import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { downloadAuditProof, exportAuditChain, listAuditEvents } from './api'
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
  // 导出整条审计链:走 session blob 下载。busy 防重复点击;notice 单独于查询错误显示。
  const [exporting, setExporting] = useState(false)
  // 正在下载证明的事件 id(行内 busy 标识);null 表示无下载中。
  const [proofBusy, setProofBusy] = useState<number | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  // 导出当前【已应用】时间段(filters.from/to)内的审计链 JSON。两者必填:缺则提示而不下发歧义请求。
  const onExport = async () => {
    setNotice(null)
    setExporting(true)
    try {
      await exportAuditChain({ from: filters.from, to: filters.to })
    } catch (e) {
      setNotice(e instanceof ApiError ? `${e.message}(${e.code})` : e instanceof Error ? e.message : '导出审计链失败')
    } finally {
      setExporting(false)
    }
  }

  // 下载单条事件的签名证明 JSON(按 request_id)。无 request_id 的事件不展示该按钮。
  const onProof = async (ev: AuditEvent) => {
    if (!ev.request_id) return
    setNotice(null)
    setProofBusy(ev.id)
    try {
      await downloadAuditProof(ev.request_id)
    } catch (e) {
      setNotice(e instanceof ApiError ? `${e.message}(${e.code})` : '下载签名证明失败')
    } finally {
      setProofBusy(null)
    }
  }

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
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>安全与审计</h1>
          <p className="hk-sub">
            管线第 8 站 · 防篡改审计账本(只读)。已载 {events.length} / 共 {total} 条。
          </p>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 4 }}>
          <button
            type="button"
            disabled={exporting || !filters.from || !filters.to}
            onClick={onExport}
            title={!filters.from || !filters.to ? '先在下方设置并应用「起 / 止」时间段,再导出该区间审计链' : '导出当前时间段内整条审计链(含签名与 Merkle 根)的 JSON'}
            className="hk-btn"
            style={{ opacity: exporting || !filters.from || !filters.to ? 0.6 : 1, cursor: exporting || !filters.from || !filters.to ? 'not-allowed' : 'pointer' }}
          >
            {exporting ? '导出中…' : '导出审计链'}
          </button>
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>导出当前已应用的「起 / 止」区间</span>
        </div>
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
          <button type="submit" className="hk-btn hk-btn--green">
            查询
          </button>
          <button type="button" onClick={() => { setDraft(EMPTY_AUDIT_FILTERS); setFilters(EMPTY_AUDIT_FILTERS) }} className="hk-btn">
            重置
          </button>
        </div>
      </form>

      {error && <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{error}</div>}
      {notice && <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{notice}</div>}

      <div className="hk-card">
        {loading && events.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : events.length === 0 ? (
          <Empty>没有匹配的审计事件。</Empty>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['时间', '事件', '严重度', '操作者', '原因', 'Request ID', ''].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {events.map((ev) => (
                  <FragmentRow
                    key={ev.id}
                    ev={ev}
                    expanded={expanded === ev.id}
                    onToggle={() => setExpanded(expanded === ev.id ? null : ev.id)}
                    proofBusy={proofBusy === ev.id}
                    onProof={() => onProof(ev)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {nextCursor && (
        <button type="button" disabled={loading} onClick={loadMore} className="hk-btn" style={{ alignSelf: 'center' }}>
          {loading ? '加载中…' : '加载更多'}
        </button>
      )}
    </div>
  )
}

function FragmentRow({
  ev,
  expanded,
  onToggle,
  proofBusy,
  onProof,
}: {
  ev: AuditEvent
  expanded: boolean
  onToggle: () => void
  proofBusy: boolean
  onProof: () => void
}) {
  return (
    <>
      <tr>
        <td className="hk-mono">{fmt(ev.created_at)}</td>
        <td>
          <div style={{ display: 'flex', flexDirection: 'column' }}>
            <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{ev.event_type}</span>
            <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{ev.event_class}</span>
          </div>
        </td>
        <td>
          <StatusBadge tone={severityTone(ev.severity)}>{ev.severity || '—'}</StatusBadge>
        </td>
        <td>{actorLabel(ev)}</td>
        <td style={{ maxWidth: 240, color: 'var(--hk-ink-700)' }}>{ev.reason || '—'}</td>
        <td className="hk-mono">{ev.request_id ? short(ev.request_id) : '—'}</td>
        <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
          {/* 仅当事件带 request_id 时可出具单条签名证明(后端按 request_id 查账本)。 */}
          {ev.request_id && (
            <button type="button" disabled={proofBusy} onClick={onProof} style={{ ...linkBtn, opacity: proofBusy ? 0.6 : 1, cursor: proofBusy ? 'wait' : 'pointer' }} title="下载该事件的签名证明 JSON">
              {proofBusy ? '下载中…' : '签名证明'}
            </button>
          )}
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
  return <div className="hk-empty">{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
