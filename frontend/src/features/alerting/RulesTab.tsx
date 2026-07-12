import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { createRule, deleteRule, listRules, updateRule } from './api'
import {
  buildCreateRule,
  buildUpdateRule,
  COMPARATORS,
  comparatorSymbol,
  EMPTY_RULE_FORM,
  filtersToText,
  METRIC_TYPES,
  SEVERITIES,
  severityLabel,
  severityTone,
  type RuleForm,
} from './alerting'
import type { AlertRule } from './types'
import { card, errBox, fmt, ghostBtn, inp, modal, newBtn, overlay, primaryBtn, td, tdTime, th, Empty, Field, linkBtn, dangerLinkBtn } from './ui'

/*
 * 告警规则 Tab。/v1/admin/alert-rules 列表 + 新建/编辑(名称/指标/比较符/阈值/级别/窗口/持续/冷却/
 * 邮件通知/启停/维度过滤)+ 行内启停 + 删除(二次确认)。tenant_id 由父页统一传入。
 */
export function RulesTab({ tenantId }: { tenantId: number }) {
  const [items, setItems] = useState<AlertRule[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [editing, setEditing] = useState<AlertRule | 'new' | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listRules(tenantId, 200, 0, signal)
        .then((resp) => setItems(resp.items))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载告警规则失败')
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

  const act = async (id: number, fn: () => Promise<unknown>) => {
    setBusyId(id)
    setError(null)
    try {
      await fn()
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusyId(null)
    }
  }

  const onToggle = (rule: AlertRule) => act(rule.id, () => updateRule(tenantId, rule.id, { enabled: !rule.enabled }))

  const onDelete = (rule: AlertRule) => {
    if (!window.confirm(`确认删除告警规则「${rule.name}」?此操作不可撤销。`)) return
    void act(rule.id, () => deleteRule(tenantId, rule.id))
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>共 {items.length} 条规则。规则在评估窗口内命中阈值即产生告警事件。</p>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
          <button type="button" onClick={refresh} style={ghostBtn}>
            刷新
          </button>
          <button type="button" onClick={() => setEditing('new')} style={newBtn}>
            ＋ 新建规则
          </button>
        </div>
      </header>

      {error && <div style={errBox}>{error}</div>}

      <div style={card}>
        {loading && items.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : items.length === 0 ? (
          <Empty>暂无告警规则。点击「新建规则」开始配置。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['规则', '触发条件', '级别', '窗口', '状态', '上次触发', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {items.map((rule) => (
                  <tr key={rule.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>
                      <div style={{ display: 'flex', flexDirection: 'column' }}>
                        <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{rule.name}</span>
                        <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{rule.metric_type || rule.metric}</span>
                      </div>
                    </td>
                    <td style={td}>
                      <code style={{ fontSize: 12, color: 'var(--hk-ink-700)' }}>
                        {comparatorSymbol(rule.comparator)} {rule.threshold}
                      </code>
                      {rule.notify_email && <span style={{ marginLeft: 6, fontSize: 11, color: 'var(--hk-ink-300)' }}>✉ 邮件</span>}
                    </td>
                    <td style={td}>
                      <StatusBadge tone={severityTone(rule.severity) as BadgeTone}>{severityLabel(rule.severity)}</StatusBadge>
                    </td>
                    <td style={tdTime}>{rule.window_seconds}s</td>
                    <td style={td}>
                      <StatusBadge tone={rule.enabled ? 'ok' : 'muted'}>{rule.enabled ? '启用' : '停用'}</StatusBadge>
                    </td>
                    <td style={tdTime}>{fmt(rule.last_triggered_at)}</td>
                    <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                      <button type="button" disabled={busyId === rule.id} onClick={() => setEditing(rule)} style={linkBtn}>
                        编辑
                      </button>
                      <button type="button" disabled={busyId === rule.id} onClick={() => onToggle(rule)} style={linkBtn}>
                        {rule.enabled ? '停用' : '启用'}
                      </button>
                      <button type="button" disabled={busyId === rule.id} onClick={() => onDelete(rule)} style={dangerLinkBtn}>
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

      {editing && (
        <RuleModal
          tenantId={tenantId}
          existing={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            refresh()
          }}
        />
      )}
    </div>
  )
}

function RuleModal({
  tenantId,
  existing,
  onClose,
  onSaved,
}: {
  tenantId: number
  existing: AlertRule | null
  onClose: () => void
  onSaved: () => void
}) {
  const [form, setForm] = useState<RuleForm>(() => toForm(existing))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const set = <K extends keyof RuleForm>(k: K, v: RuleForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    setError(null)
    try {
      if (existing) {
        const built = buildUpdateRule(form)
        if ('error' in built) {
          setError(built.error)
          return
        }
        setBusy(true)
        await updateRule(tenantId, existing.id, built)
      } else {
        const built = buildCreateRule(form, tenantId)
        if ('error' in built) {
          setError(built.error)
          return
        }
        setBusy(true)
        await createRule(built)
      }
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
        <h2 style={{ fontSize: 18 }}>{existing ? '编辑告警规则' : '新建告警规则'}</h2>
        <Field label="规则名称">
          <input value={form.name} onChange={(e) => set('name', e.target.value)} style={inp} />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--hk-space-3)' }}>
          <Field label="指标类型">
            <select value={form.metricType} onChange={(e) => set('metricType', e.target.value)} style={inp}>
              {METRIC_TYPES.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="指标名(自定义时填)">
            <input value={form.metric} onChange={(e) => set('metric', e.target.value)} placeholder="如 error_rate" style={inp} />
          </Field>
        </div>
        <div style={{ display: 'flex', gap: 'var(--hk-space-3)' }}>
          <Field label="比较符" flex={1}>
            <select value={form.comparator} onChange={(e) => set('comparator', e.target.value as RuleForm['comparator'])} style={inp}>
              {COMPARATORS.map((c) => (
                <option key={c.value} value={c.value}>
                  {c.symbol} {c.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="阈值" flex={1}>
            <input value={form.threshold} inputMode="decimal" onChange={(e) => set('threshold', e.target.value)} style={inp} />
          </Field>
          <Field label="级别" flex={1}>
            <select value={form.severity} onChange={(e) => set('severity', e.target.value as RuleForm['severity'])} style={inp}>
              {SEVERITIES.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </select>
          </Field>
        </div>
        <div style={{ display: 'flex', gap: 'var(--hk-space-3)' }}>
          <Field label="观察窗口(秒)">
            <input value={form.windowSeconds} inputMode="numeric" onChange={(e) => set('windowSeconds', e.target.value)} style={inp} />
          </Field>
          <Field label="持续(秒,可选)">
            <input value={form.sustainedSeconds} inputMode="numeric" onChange={(e) => set('sustainedSeconds', e.target.value)} style={inp} />
          </Field>
          <Field label="冷却(秒,可选)">
            <input value={form.cooldownSeconds} inputMode="numeric" onChange={(e) => set('cooldownSeconds', e.target.value)} style={inp} />
          </Field>
        </div>
        <Field label="维度过滤(可选,每行 键=值)">
          <textarea
            value={form.filtersText}
            onChange={(e) => set('filtersText', e.target.value)}
            rows={2}
            placeholder="platform=anthropic&#10;region=us"
            style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', resize: 'vertical' }}
          />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--hk-space-4)', alignItems: 'center' }}>
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--hk-ink-700)' }}>
            <input type="checkbox" checked={form.notifyEmail} onChange={(e) => set('notifyEmail', e.target.checked)} />
            触发时发送邮件
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--hk-ink-700)' }}>
            <input type="checkbox" checked={form.enabled} onChange={(e) => set('enabled', e.target.checked)} />
            启用规则
          </label>
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

/** 把已有规则回填成表单(数值转串)。新建则用空表单。 */
function toForm(rule: AlertRule | null): RuleForm {
  if (!rule) return EMPTY_RULE_FORM
  return {
    name: rule.name,
    metric: rule.metric,
    metricType: rule.metric_type ?? '',
    comparator: (COMPARATORS.find((c) => c.value === rule.comparator)?.value ?? 'gt'),
    threshold: String(rule.threshold),
    severity: (SEVERITIES.find((s) => s.value === rule.severity)?.value ?? 'warning'),
    windowSeconds: String(rule.window_seconds),
    sustainedSeconds: String(rule.sustained_seconds),
    cooldownSeconds: String(rule.cooldown_seconds),
    notifyEmail: rule.notify_email,
    enabled: rule.enabled,
    filtersText: filtersToText(rule.filters),
  }
}
