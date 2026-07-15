import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { createQuotaPolicy, updateQuotaPolicy } from './api'
import {
  METRICS,
  MODES,
  SCOPE_KINDS,
  WINDOW_KINDS,
  isEnforce,
  metricLabel,
  modeLabel,
  scopeKindLabel,
  validatePolicyForm,
  windowKindLabel,
} from './quotapolicies'
import type { Metric, Mode, PolicyForm, QuotaPolicy, ScopeKind, WindowKind } from './types'

/*
 * 配额策略新建/编辑表单(模态)。复用同一套 UI:新建 vs 编辑由 existing 是否存在决定。
 * 校验走 validatePolicyForm(镜像后端 validateRequest);limit_value/burst_value 字符串原样回传(防精度丢失)。
 * 高影响:mode=enforce(真会拦请求)保存前二次确认;reason 输入供审计。
 */

interface Props {
  tenantId: number
  /** 编辑时传入已存在策略(用于 PUT);新建时为 null。 */
  existing: QuotaPolicy | null
  /** 表单初值(新建=emptyPolicyForm,编辑=policyToForm)。 */
  initial: PolicyForm
  onClose: () => void
  onSaved: (p: QuotaPolicy) => void
}

export function QuotaPolicyForm({ tenantId, existing, initial, onClose, onSaved }: Props) {
  const [form, setForm] = useState<PolicyForm>(initial)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const setF = <K extends keyof PolicyForm>(k: K, v: PolicyForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    const v = validatePolicyForm(form)
    if (!v.ok) {
      setError(v.error)
      return
    }
    // 高影响二次确认:enforce 模式真会拦截请求(可能影响在线流量)。
    if (isEnforce(v.value.mode)) {
      const verb = existing ? '更新' : '新建'
      if (
        !window.confirm(
          `该策略为「强制拦截(enforce)」模式,保存后会真实拦截命中的请求。确认${verb}?`,
        )
      ) {
        return
      }
    }
    setSaving(true)
    setError(null)
    try {
      const saved = existing
        ? await updateQuotaPolicy(existing.id, tenantId, v.value)
        : await createQuotaPolicy(tenantId, v.value)
      onSaved(saved)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={overlay} onClick={onClose}>
      <div style={modal} onClick={(e) => e.stopPropagation()}>
        <div style={modalHead}>
          <h2 style={{ fontSize: 16, margin: 0 }}>{existing ? `编辑策略 #${existing.id}` : '新建配额策略'}</h2>
          <button type="button" onClick={onClose} style={closeBtn} aria-label="关闭">
            ×
          </button>
        </div>

        {error && <Banner>{error}</Banner>}

        <div style={grid}>
          <SelectField
            label="作用域类型(scope_kind)"
            value={form.scopeKind}
            options={SCOPE_KINDS.map((v) => ({ value: v, label: scopeKindLabel(v) }))}
            onChange={(v) => setF('scopeKind', v as ScopeKind)}
          />
          <Field label="作用域 ID(scope_id,全局用 *)">
            <input value={form.scopeId} onChange={(e) => setF('scopeId', e.target.value)} placeholder="如 u-42 或 *" style={inp} />
          </Field>
          <SelectField
            label="指标(metric)"
            value={form.metric}
            options={METRICS.map((v) => ({ value: v, label: metricLabel(v) }))}
            onChange={(v) => setF('metric', v as Metric)}
          />
          <SelectField
            label="窗口类型(window_kind)"
            value={form.windowKind}
            options={WINDOW_KINDS.map((v) => ({ value: v, label: windowKindLabel(v) }))}
            onChange={(v) => setF('windowKind', v as WindowKind)}
          />
          <Field label="窗口秒数(window_seconds,固定窗口须 >0)">
            <input value={form.windowSeconds} onChange={(e) => setF('windowSeconds', e.target.value)} inputMode="numeric" placeholder="如 3600" style={inp} />
          </Field>
          <Field label="上限(limit_value,十进制)">
            <input value={form.limitValue} onChange={(e) => setF('limitValue', e.target.value)} inputMode="decimal" placeholder="如 1000 或 5.50" style={{ ...inp, fontFamily: 'var(--hk-font-mono)' }} />
          </Field>
          <Field label="突发上限(burst_value,可空=0)" help="窗口内实际上限=上限+突发；0=无突发">
            <input value={form.burstValue} onChange={(e) => setF('burstValue', e.target.value)} inputMode="decimal" placeholder="留空=0" style={{ ...inp, fontFamily: 'var(--hk-font-mono)' }} />
          </Field>
          <SelectField
            label="模式(mode)"
            value={form.mode}
            options={MODES.map((v) => ({ value: v, label: modeLabel(v) }))}
            onChange={(v) => setF('mode', v as Mode)}
          />
          <Field label="优先级(priority,整数)">
            <input value={form.priority} onChange={(e) => setF('priority', e.target.value)} inputMode="numeric" placeholder="如 100" style={inp} />
          </Field>
          <Field label="生效时间(valid_from,RFC3339,可空=立即)">
            <input value={form.validFrom} onChange={(e) => setF('validFrom', e.target.value)} placeholder="2026-01-01T00:00:00Z" style={inp} />
          </Field>
          <Field label="失效时间(valid_until,RFC3339,可空=永久)">
            <input value={form.validUntil} onChange={(e) => setF('validUntil', e.target.value)} placeholder="留空=永久" style={inp} />
          </Field>
          <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 13, color: 'var(--hk-ink-700)', cursor: 'pointer', height: 32, alignSelf: 'flex-end' }}>
            <input type="checkbox" checked={form.enabled} onChange={(e) => setF('enabled', e.target.checked)} /> 启用(enabled)
          </label>
          <Field label="变更原因(reason,供审计,可空)">
            <input value={form.reason} onChange={(e) => setF('reason', e.target.value)} placeholder="如 防滥用收紧" style={inp} />
          </Field>
        </div>

        {isEnforce(form.mode) && (
          <div style={enforceHint}>注意:强制拦截(enforce)模式会真实拦截命中的请求,保存前会再次确认。</div>
        )}

        <div style={modalFoot}>
          <button type="button" onClick={onClose} disabled={saving} style={ghostBtn}>
            取消
          </button>
          <button type="button" onClick={submit} disabled={saving} style={primaryBtn}>
            {saving ? '保存中…' : existing ? '保存修改' : '创建策略'}
          </button>
        </div>
      </div>
    </div>
  )
}

/* ——— 本文件私有小组件 / 样式 ——— */
function Field({ label, help, children }: { label: string; help?: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
      {help && <span style={{ lineHeight: 1.4, color: 'var(--hk-ink-500)' }}>{help}</span>}
    </label>
  )
}
function SelectField({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: string
  options: Array<{ value: string; label: string }>
  onChange: (v: string) => void
}) {
  return (
    <Field label={label}>
      <select value={value} onChange={(e) => onChange(e.target.value)} style={inp}>
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </Field>
  )
}
function Banner({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>
      {children}
    </div>
  )
}

const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(15,23,42,0.45)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-8)', overflowY: 'auto', zIndex: 50 }
const modal: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-2)', width: 'min(720px, 100%)', overflow: 'hidden' }
const modalHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }
const modalFoot: React.CSSProperties = { display: 'flex', justifyContent: 'flex-end', gap: 'var(--hk-space-2)', padding: 'var(--hk-space-4)', borderTop: '1px solid var(--hk-line)' }
const grid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 'var(--hk-space-4)', padding: 'var(--hk-space-4)' }
const enforceHint: React.CSSProperties = { margin: '0 var(--hk-space-4) var(--hk-space-4)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 12, color: 'var(--hk-warn)', background: 'var(--hk-warn-soft)', border: '1px solid var(--hk-warn-soft)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const closeBtn: React.CSSProperties = { border: 'none', background: 'transparent', fontSize: 22, lineHeight: 1, color: 'var(--hk-ink-500)', cursor: 'pointer', padding: 0, width: 28, height: 28 }
