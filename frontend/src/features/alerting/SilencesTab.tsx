import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { createSilence, deleteSilence, listSilences } from './api'
import { buildCreateSilence, EMPTY_SILENCE_FORM, silenceActive, type SilenceForm } from './alerting'
import type { AlertSilence } from './types'
import { card, dangerLinkBtn, errBox, fmt, ghostBtn, inp, modal, newBtn, overlay, primaryBtn, td, tdTime, th, Empty, Field } from './ui'

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
    if (!window.confirm(`确认删除静默规则 #${s.id}(${s.reason})?`)) return
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

  const scope = (s: AlertSilence): string => {
    const parts: string[] = []
    if (s.rule_id != null) parts.push(`规则#${s.rule_id}`)
    if (s.platform) parts.push(s.platform)
    if (s.group_id) parts.push(`组:${s.group_id}`)
    if (s.region) parts.push(s.region)
    return parts.length ? parts.join(' · ') : '全局'
  }

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

      <div style={card}>
        {loading && items.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : items.length === 0 ? (
          <Empty>暂无静默规则。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['静默', '原因', '作用域', '生效态', '开始', '结束', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {items.map((s) => (
                  <tr key={s.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>#{s.id}</td>
                    <td style={td}>
                      <span style={{ color: 'var(--hk-ink-900)' }}>{s.reason || '—'}</span>
                    </td>
                    <td style={td}>
                      <span style={{ fontSize: 12, color: 'var(--hk-ink-700)' }}>{scope(s)}</span>
                    </td>
                    <td style={td}>
                      {silenceActive(s) ? (
                        <StatusBadge tone="warn">静默中</StatusBadge>
                      ) : (
                        <StatusBadge tone="muted">未生效</StatusBadge>
                      )}
                    </td>
                    <td style={tdTime}>{fmt(s.starts_at)}</td>
                    <td style={tdTime}>{fmt(s.ends_at)}</td>
                    <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                      <button type="button" disabled={busyId === s.id} onClick={() => onDelete(s)} style={dangerLinkBtn}>
                        删除
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
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
