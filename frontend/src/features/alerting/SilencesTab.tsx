import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatCard } from '../../ui/StatCard'
import { StatusBadge } from '../../ui/StatusBadge'
import { confirmIrreversible } from '../../ui/confirmDanger'
import { createSilence, deleteSilence, listSilences } from './api'
import {
  buildCreateSilence,
  EMPTY_SILENCE_FORM,
  mapAlertResourceStat,
  mapAlertSilenceRows,
  type AlertSilenceTableRow,
  type SilenceForm,
} from './alerting'
import type { AlertSilence } from './types'
import { card, errBox, ghostBtn, inp, modal, newBtn, overlay, primaryBtn, Field } from './ui'

/*
 * 告警静默 Tab。/v1/admin/alert-silences 列表(生效态徽章/时间窗/作用域)+ 新建(原因/起止/
 * 可选规则/平台/分组/区域)+ 删除(二次确认)。静默期内匹配的告警不发通知。tenant_id 由父页传入。
 */
export function SilencesTab({ tenantId }: { tenantId: number }) {
  const [items, setItems] = useState<AlertSilence[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [creating, setCreating] = useState(false)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listSilences(tenantId, 200, 0, signal)
        .then((resp) => setItems(resp.items))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载静默规则失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [tenantId, refreshNonce],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const refresh = () => setRefreshNonce((n) => n + 1)

  const onDelete = (s: AlertSilence) => {
    if (!confirmIrreversible(`删除静默规则 #${s.id}(${s.reason})`)) return
    void (async () => {
      setBusyId(s.id)
      setError(null)
      try {
        await deleteSilence(tenantId, s.id)
        refresh()
      } catch (e) {
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '删除失败')
      } finally {
        setBusyId(null)
      }
    })()
  }

  const rows = mapAlertSilenceRows(items)
  const countStat = mapAlertResourceStat('静默规则', items.length)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>共 {items.length} 条静默。静默窗口内匹配的告警不发送通知。</p>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
          <button type="button" onClick={refresh} style={ghostBtn}>
            刷新
          </button>
          <button type="button" onClick={() => setCreating(true)} style={newBtn}>
            ＋ 新建静默
          </button>
        </div>
      </header>

      {error && <div style={errBox}>{error}</div>}

      <div style={statGrid}><StatCard {...countStat} /></div>

      <div style={card}>
        {loading && items.length === 0 ? (
          <EmptyState title="正在加载静默规则" hint="请稍候。" />
        ) : items.length === 0 ? (
          <EmptyState title="暂无静默规则" hint="可按维护窗口新建临时静默。" />
        ) : (
          <DataListTable
            label="静默规则"
            rows={rows}
            rowKey={(row) => row.id}
            columns={silenceColumns}
            actions={[{
              label: '删除',
              tone: 'danger',
              onClick: (row) => onDelete(row.source),
              disabled: (row) => busyId === row.id,
            }]}
          />
        )}
      </div>

      {creating && (
        <SilenceModal
          tenantId={tenantId}
          onClose={() => setCreating(false)}
          onSaved={() => {
            setCreating(false)
            refresh()
          }}
        />
      )}
    </div>
  )
}

const silenceColumns: DataListColumn<AlertSilenceTableRow>[] = [
  { key: 'silence', label: '静默', render: (row) => `#${row.id}` },
  { key: 'reason', label: '原因', render: (row) => row.reason },
  { key: 'scope', label: '作用域', render: (row) => row.scope },
  { key: 'active', label: '生效态', badge: true, render: (row) => <StatusBadge tone={row.active ? 'warn' : 'muted'}>{row.active ? '静默中' : '未生效'}</StatusBadge> },
  { key: 'starts-at', label: '开始', render: (row) => row.startsAt },
  { key: 'ends-at', label: '结束', render: (row) => row.endsAt },
]

const statGrid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'minmax(160px,240px)' }

function SilenceModal({ tenantId, onClose, onSaved }: { tenantId: number; onClose: () => void; onSaved: () => void }) {
  const [form, setForm] = useState<SilenceForm>(EMPTY_SILENCE_FORM)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const set = <K extends keyof SilenceForm>(k: K, v: SilenceForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    setError(null)
    const built = buildCreateSilence(form, tenantId)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setBusy(true)
    try {
      await createSilence(built)
      onSaved()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div onClick={onClose} style={overlay}>
      <div onClick={(e) => e.stopPropagation()} style={modal}>
        <h2 style={{ fontSize: 18 }}>新建静默</h2>
        <Field label="原因">
          <input value={form.reason} onChange={(e) => set('reason', e.target.value)} placeholder="如 上游维护窗口" style={inp} />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--hk-space-3)' }}>
          <Field label="开始时间">
            <input type="datetime-local" value={form.startsAt} onChange={(e) => set('startsAt', e.target.value)} style={inp} />
          </Field>
          <Field label="结束时间(须晚于开始)">
            <input type="datetime-local" value={form.endsAt} onChange={(e) => set('endsAt', e.target.value)} style={inp} />
          </Field>
        </div>
        <Field label="限定规则 ID(可选,留空=全局静默)">
          <input value={form.ruleId} inputMode="numeric" onChange={(e) => set('ruleId', e.target.value)} placeholder="全部规则" style={inp} />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--hk-space-3)' }}>
          <Field label="平台(可选)">
            <input value={form.platform} onChange={(e) => set('platform', e.target.value)} style={inp} />
          </Field>
          <Field label="分组(可选)">
            <input value={form.groupId} onChange={(e) => set('groupId', e.target.value)} style={inp} />
          </Field>
          <Field label="区域(可选)">
            <input value={form.region} onChange={(e) => set('region', e.target.value)} style={inp} />
          </Field>
        </div>
        {error && <div style={errBox}>{error}</div>}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} style={ghostBtn}>
            取消
          </button>
          <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </div>
  )
}
