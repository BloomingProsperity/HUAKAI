import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatCard } from '../../ui/StatCard'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { confirmIrreversible } from '../../ui/confirmDanger'
import { createRule, deleteRule, fetchMetricCatalog, listRules, updateRule } from './api'
import {
  buildCreateRule,
  buildUpdateRule,
  COMPARATORS,
  EMPTY_RULE_FORM,
  filtersToText,
  mapAlertResourceStat,
  mapAlertRuleRows,
  SEVERITIES,
  type RuleForm,
  type AlertRuleTableRow,
} from './alerting'
import type { AlertMetricCatalogEntry, AlertRule } from './types'
import { card, errBox, ghostBtn, inp, modal, newBtn, overlay, primaryBtn, Field } from './ui'

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
  const [metricCatalog, setMetricCatalog] = useState<AlertMetricCatalogEntry[]>([])
  const [metricCatalogWarning, setMetricCatalogWarning] = useState<string | null>(null)

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

  useEffect(() => {
    const ctrl = new AbortController()
    loadMetricCatalogState(ctrl.signal).then((state) => {
      if (ctrl.signal.aborted) return
      setMetricCatalog(state.entries)
      setMetricCatalogWarning(state.warning)
    })
    return () => ctrl.abort()
  }, [])

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
    if (!confirmIrreversible(`删除告警规则「${rule.name}」`)) return
    void act(rule.id, () => deleteRule(tenantId, rule.id))
  }

  const rows = mapAlertRuleRows(items)
  const countStat = mapAlertResourceStat('告警规则', items.length)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>共 {items.length} 条规则。usage.* 指标按每条规则的统计窗口聚合后与阈值比较。</p>
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

      <div style={statGrid}><StatCard {...countStat} /></div>

      <div style={card}>
        {loading && items.length === 0 ? (
          <EmptyState title="正在加载告警规则" hint="请稍候。" />
        ) : items.length === 0 ? (
          <EmptyState title="暂无告警规则" hint="点击「新建规则」开始配置。" />
        ) : (
          <DataListTable
            label="告警规则"
            rows={rows}
            rowKey={(row) => row.id}
            columns={ruleColumns}
            actions={[
              { label: '编辑', onClick: (row) => setEditing(row.source), disabled: (row) => busyId === row.id },
              { label: (row) => row.enabled ? '停用' : '启用', onClick: (row) => void onToggle(row.source), disabled: (row) => busyId === row.id },
              { label: '删除', tone: 'danger', onClick: (row) => onDelete(row.source), disabled: (row) => busyId === row.id },
            ]}
          />
        )}
      </div>

      {editing && (
        <RuleModal
          tenantId={tenantId}
          existing={editing === 'new' ? null : editing}
          metricCatalog={metricCatalog}
          metricCatalogWarning={metricCatalogWarning}
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

const ruleColumns: DataListColumn<AlertRuleTableRow>[] = [
  { key: 'rule', label: '规则', render: (row) => <span style={{ display: 'flex', flexDirection: 'column' }}><strong>{row.name}</strong><small>{row.metric}</small></span> },
  { key: 'condition', label: '触发条件', render: (row) => <code>{row.condition}{row.email ? ' · ✉ 邮件' : ''}</code> },
  { key: 'severity', label: '级别', badge: true, render: (row) => <StatusBadge tone={row.severityTone as BadgeTone}>{row.severity}</StatusBadge> },
  { key: 'window', label: '窗口', render: (row) => row.window },
  { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.enabled ? 'ok' : 'muted'}>{row.enabled ? '启用' : '停用'}</StatusBadge> },
  { key: 'last-triggered', label: '上次触发', render: (row) => row.lastTriggeredAt },
]

const statGrid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'minmax(160px,240px)' }

function RuleModal({
  tenantId,
  existing,
  metricCatalog,
  metricCatalogWarning,
  onClose,
  onSaved,
}: {
  tenantId: number
  existing: AlertRule | null
  metricCatalog: AlertMetricCatalogEntry[]
  metricCatalogWarning: string | null
  onClose: () => void
  onSaved: () => void
}) {
  const [form, setForm] = useState<RuleForm>(() => toForm(existing))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const set = <K extends keyof RuleForm>(k: K, v: RuleForm[K]) => setForm((f) => ({ ...f, [k]: v }))
  const selectedCatalogEntry = catalogEntryForMetric(metricCatalog, form.metric)

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
          <Field label="内建指标">
            <MetricCatalogSelect
              entries={metricCatalog}
              metric={form.metric}
              warning={metricCatalogWarning}
              onMetricChange={(metric) => set('metric', metric)}
            />
          </Field>
          <Field label="指标名">
            <input
              value={form.metric}
              onChange={(e) => set('metric', e.target.value)}
              placeholder={selectedCatalogEntry?.is_prefix
                ? `请在 ${selectedCatalogEntry.name} 后补全状态，如 account.unhealthy_throttled`
                : '如 custom.metric'}
              style={inp}
            />
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
          <Field label="统计窗口(秒)：usage.* 指标按此窗口聚合后与阈值比较">
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

export interface MetricCatalogLoadState {
  entries: AlertMetricCatalogEntry[]
  warning: string | null
}

/** 目录失败只降级为自定义指标，不阻断规则表单。 */
export async function loadMetricCatalogState(signal?: AbortSignal): Promise<MetricCatalogLoadState> {
  try {
    return { entries: await fetchMetricCatalog(signal), warning: null }
  } catch {
    return { entries: [], warning: '指标目录加载失败，仍可使用自定义指标名。' }
  }
}

export function catalogEntryForMetric(
  entries: AlertMetricCatalogEntry[],
  metric: string,
): AlertMetricCatalogEntry | undefined {
  return entries.find((entry) => entry.is_prefix ? metric.startsWith(entry.name) : metric === entry.name)
}

export function MetricCatalogSelect({
  entries,
  metric,
  warning,
  onMetricChange,
}: {
  entries: AlertMetricCatalogEntry[]
  metric: string
  warning: string | null
  onMetricChange: (metric: string) => void
}) {
  const selected = catalogEntryForMetric(entries, metric)?.name ?? ''
  return (
    <>
      <select value={selected} onChange={(e) => onMetricChange(e.target.value)} style={inp}>
        <option value="">自定义(用指标名)</option>
        {entries.map((entry) => (
          <option key={entry.name} value={entry.name}>
            {entry.label}（{entry.name} · {entry.unit}）
          </option>
        ))}
      </select>
      {warning && <small style={{ color: 'var(--hk-ink-500)', fontSize: 12 }}>{warning}</small>}
    </>
  )
}
