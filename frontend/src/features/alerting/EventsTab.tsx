import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { listEvents, manualResolveEvent } from './api'
import { eventStateLabel, eventStateTone, EVENT_STATES, isFiring } from './alerting'
import type { AlertEvent, EventState } from './types'
import { card, errBox, fmt, ghostBtn, inp, td, tdTime, th, Empty, Field, linkBtn } from './ui'

/*
 * 告警事件 Tab。/v1/admin/alert-events 列表(状态徽章/观测值/阈值/触发时间)+ 状态过滤
 * + 规则 ID 过滤 + 手动恢复(仅 firing 可恢复)。tenant_id 由父页统一传入。
 */
export function EventsTab({ tenantId }: { tenantId: number }) {
  const [items, setItems] = useState<AlertEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [state, setState] = useState<EventState | ''>('')
  const [ruleId, setRuleId] = useState('')
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      const rid = /^\d+$/.test(ruleId.trim()) ? Number.parseInt(ruleId.trim(), 10) : undefined
      listEvents(tenantId, { state: state || undefined, ruleId: rid }, signal)
        .then((resp) => setItems(resp.items))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载告警事件失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [tenantId, state, ruleId, refreshNonce],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const refresh = () => setRefreshNonce((n) => n + 1)

  const onResolve = async (ev: AlertEvent) => {
    if (!window.confirm(`确认手动恢复规则 #${ev.rule_id} 的告警事件 #${ev.id}?`)) return
    setBusyId(ev.id)
    setError(null)
    try {
      await manualResolveEvent(tenantId, ev.id)
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '手动恢复失败')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <form onSubmit={(e) => e.preventDefault()} style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)', alignItems: 'flex-end' }}>
        <Field label="状态" flex={0}>
          <select value={state} onChange={(e) => setState(e.target.value as EventState | '')} style={{ ...inp, width: 140 }}>
            <option value="">全部</option>
            {EVENT_STATES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="规则 ID(可选)" flex={0}>
          <input value={ruleId} inputMode="numeric" onChange={(e) => setRuleId(e.target.value)} placeholder="全部" style={{ ...inp, width: 120 }} />
        </Field>
        <button type="button" onClick={refresh} style={ghostBtn}>
          刷新
        </button>
        <span style={{ marginLeft: 'auto', fontSize: 13, color: 'var(--hk-ink-500)' }}>共 {items.length} 条事件</span>
      </form>

      {error && <div style={errBox}>{error}</div>}

      <div style={card}>
        {loading && items.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : items.length === 0 ? (
          <Empty>没有匹配的告警事件。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['事件', '规则', '状态', '观测值 / 阈值', '邮件', '触发时间', '恢复时间', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {items.map((ev) => (
                  <tr key={ev.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>#{ev.id}</td>
                    <td style={td}>#{ev.rule_id}</td>
                    <td style={td}>
                      <StatusBadge tone={eventStateTone(ev.state) as BadgeTone}>{eventStateLabel(ev.state)}</StatusBadge>
                    </td>
                    <td style={td}>
                      <code style={{ fontSize: 12, color: 'var(--hk-ink-700)' }}>
                        {ev.observed_value}
                        {ev.threshold_value != null ? ` / ${ev.threshold_value}` : ''}
                      </code>
                    </td>
                    <td style={td}>{ev.email_sent ? '已发' : '—'}</td>
                    <td style={tdTime}>{fmt(ev.fired_at)}</td>
                    <td style={tdTime}>{fmt(ev.resolved_at)}</td>
                    <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                      {isFiring(ev.state) ? (
                        <button type="button" disabled={busyId === ev.id} onClick={() => onResolve(ev)} style={linkBtn}>
                          手动恢复
                        </button>
                      ) : (
                        <span style={{ color: 'var(--hk-ink-300)', fontSize: 12 }}>—</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
