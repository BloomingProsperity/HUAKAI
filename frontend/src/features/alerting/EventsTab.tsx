import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatCard } from '../../ui/StatCard'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { confirmIrreversible } from '../../ui/confirmDanger'
import { listEvents, manualResolveEvent } from './api'
import { EVENT_STATES, mapAlertEventRows, mapAlertResourceStat, type AlertEventTableRow } from './alerting'
import type { AlertEvent, EventState } from './types'
import { card, errBox, ghostBtn, inp, Field } from './ui'

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
    if (!confirmIrreversible(`手动恢复规则 #${ev.rule_id} 的告警事件 #${ev.id}`, '恢复后该事件不再处于触发中。')) return
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

  const rows = mapAlertEventRows(items)
  const countStat = mapAlertResourceStat('告警事件', items.length)

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

      <div style={statGrid}><StatCard {...countStat} /></div>

      <div style={card}>
        {loading && items.length === 0 ? (
          <EmptyState title="正在加载告警事件" hint="请稍候。" />
        ) : items.length === 0 ? (
          <EmptyState title="没有匹配的告警事件" hint="可调整状态或规则 ID 过滤条件。" />
        ) : (
          <DataListTable
            label="告警事件"
            rows={rows}
            rowKey={(row) => row.id}
            columns={eventColumns}
            actions={[{
              label: (row) => row.canResolve ? '手动恢复' : '已结束',
              onClick: (row) => void onResolve(row.source),
              disabled: (row) => !row.canResolve || busyId === row.id,
            }]}
          />
        )}
      </div>
    </div>
  )
}

const eventColumns: DataListColumn<AlertEventTableRow>[] = [
  { key: 'event', label: '事件', render: (row) => `#${row.id}` },
  { key: 'rule', label: '规则', render: (row) => `#${row.ruleID}` },
  { key: 'state', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.stateTone as BadgeTone}>{row.state}</StatusBadge> },
  { key: 'value', label: '观测值 / 阈值', render: (row) => <code>{row.observedThreshold}</code> },
  { key: 'email', label: '邮件', render: (row) => row.email },
  { key: 'fired-at', label: '触发时间', render: (row) => row.firedAt },
  { key: 'resolved-at', label: '恢复时间', render: (row) => row.resolvedAt },
]

const statGrid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'minmax(160px,240px)' }
