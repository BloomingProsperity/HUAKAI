import { useEffect, useMemo, useState, type CSSProperties } from 'react'
import { ApiError } from '../../lib/api'
import { listProxies } from '../proxies/api'
import type { Proxy } from '../proxies/types'
import {
  ACCOUNT_ADVANCED_FIELD_SPECS,
  ADVANCED_BOOLEAN_FORM_KEYS,
  ADVANCED_NUMBER_FORM_KEYS,
  type AccountAdvancedFormState,
  type AdvancedFieldSpec,
  type NullableFieldMode,
} from './advancedFields'
import type { ProviderAccount } from './types'

type AdvancedSetter = <K extends keyof AccountAdvancedFormState>(
  key: K,
  value: AccountAdvancedFormState[K],
) => void

interface Props {
  mode: 'create' | 'edit'
  tenantId: number | null
  form: AccountAdvancedFormState
  onChange: AdvancedSetter
  current?: ProviderAccount
  defaultOpen?: boolean
}

interface ProxyGroupSummary {
  groupId: string
  total: number
  active: number
}

/** create/edit 共用的数据驱动高级设置区；每个规范条目只渲染一个带 key 标记的控件组。 */
export function AccountAdvancedSettings({
  mode,
  tenantId,
  form,
  onChange,
  current,
  defaultOpen = false,
}: Props) {
  const [proxies, setProxies] = useState<Proxy[]>([])
  const [proxyError, setProxyError] = useState<string | null>(null)
  const [proxyLoading, setProxyLoading] = useState(false)

  useEffect(() => {
    if (tenantId == null) return
    const controller = new AbortController()
    setProxyLoading(true)
    setProxyError(null)
    listProxies(tenantId, controller.signal)
      .then((response) => setProxies(response.items))
      .catch((error: unknown) => {
        if (controller.signal.aborted) return
        setProxyError(error instanceof ApiError ? `${error.message}(${error.code})` : '加载代理列表失败')
      })
      .finally(() => {
        if (!controller.signal.aborted) setProxyLoading(false)
      })
    return () => controller.abort()
  }, [tenantId])

  const proxyGroups = useMemo(() => summarizeProxyGroups(proxies), [proxies])
  const selectedGroup = proxyGroups.find((group) => group.groupId === form.proxyGroupId.trim()) ?? {
    groupId: form.proxyGroupId.trim(),
    total: 0,
    active: 0,
  }
  const groupListID = `account-proxy-groups-${mode}-${current?.id ?? 'new'}`

  const setRule = (index: number, key: keyof AccountAdvancedFormState['tempUnschedulableRules'][number], value: string) => {
    onChange(
      'tempUnschedulableRules',
      form.tempUnschedulableRules.map((rule, ruleIndex) =>
        ruleIndex === index ? { ...rule, [key]: value } : rule,
      ),
    )
  }

  const addRule = () => {
    onChange('tempRulesMode', 'replace')
    onChange('tempUnschedulableRules', [
      ...form.tempUnschedulableRules,
      { errorCode: '', keywords: '', durationMinutes: '', description: '' },
    ])
  }

  return (
    <details open={defaultOpen || undefined} style={detailsStyle}>
      <summary style={summaryStyle}>高级设置</summary>
      <p style={introStyle}>
        {mode === 'create'
          ? '留空或“不设置”时沿用数据库默认；0 表示明确的不限值。'
          : '数值框已回填当前值；留空表示不改。可空字段用写入方式明确选择“保持、设置、清除”。'}
      </p>
      <div style={gridStyle}>
        {ACCOUNT_ADVANCED_FIELD_SPECS.map((spec) => (
          <div
            key={spec.key}
            data-advanced-field={spec.key}
            data-advanced-kind={spec.kind}
            style={fieldCardStyle}
          >
            {renderField(spec, {
              mode,
              form,
              onChange,
              current,
              proxies,
              proxyGroups,
              selectedGroup,
              groupListID,
              proxyLoading,
              proxyError,
              setRule,
              addRule,
            })}
            <p style={helpStyle}>{spec.help}</p>
          </div>
        ))}
      </div>
    </details>
  )
}

interface RenderContext {
  mode: 'create' | 'edit'
  form: AccountAdvancedFormState
  onChange: AdvancedSetter
  current?: ProviderAccount
  proxies: Proxy[]
  proxyGroups: ProxyGroupSummary[]
  selectedGroup: ProxyGroupSummary
  groupListID: string
  proxyLoading: boolean
  proxyError: string | null
  setRule: (index: number, key: keyof AccountAdvancedFormState['tempUnschedulableRules'][number], value: string) => void
  addRule: () => void
}

function renderField(spec: AdvancedFieldSpec, ctx: RenderContext) {
  switch (spec.kind) {
    case 'integer':
      return renderInteger(spec, ctx)
    case 'boolean':
      return renderBoolean(spec, ctx)
    case 'datetime':
      return renderDateTime(spec, ctx)
    case 'integer_array':
      return renderErrorCodes(spec, ctx)
    case 'rule_array':
      return renderRules(spec, ctx)
    case 'proxy_binding':
      return renderProxy(spec, ctx)
  }
}

function renderInteger(spec: AdvancedFieldSpec, { form, onChange, current }: RenderContext) {
  if (spec.key === 'refresh_lead_seconds') {
    return (
      <>
        <FieldLabel spec={spec} current={current?.refresh_lead_seconds ?? null} />
        <NullableModeSelect value={form.refreshLeadMode} onChange={(value) => onChange('refreshLeadMode', value)} />
        {form.refreshLeadMode === 'value' && (
          <input
            value={form.refreshLeadSeconds}
            onChange={(event) => onChange('refreshLeadSeconds', event.target.value)}
            inputMode="numeric"
            min={spec.minimum}
            max={spec.format === 'int64' ? Number.MAX_SAFE_INTEGER : spec.maximum}
            style={inputStyle}
          />
        )}
      </>
    )
  }
  const formKey = ADVANCED_NUMBER_FORM_KEYS[spec.key as keyof typeof ADVANCED_NUMBER_FORM_KEYS]
  if (!formKey) return <p role="alert">字段缺少数值映射</p>
  return (
    <>
      <FieldLabel spec={spec} current={current?.[spec.key as keyof ProviderAccount] as number | undefined} />
      <input
        value={form[formKey]}
        onChange={(event) => onChange(formKey, event.target.value)}
        inputMode="numeric"
        min={spec.minimum}
        max={spec.format === 'int64' ? Number.MAX_SAFE_INTEGER : spec.maximum}
        placeholder="留空不设置"
        style={inputStyle}
      />
    </>
  )
}

function renderBoolean(spec: AdvancedFieldSpec, { form, onChange, current }: RenderContext) {
  if (spec.key === 'pool_mode') {
    return (
      <>
        <FieldLabel spec={spec} current={current?.pool_mode} />
        <select value={form.poolMode} onChange={(event) => onChange('poolMode', event.target.value as AccountAdvancedFormState['poolMode'])} style={inputStyle}>
          <option value="unchanged">不设置 / 保持当前</option>
          <option value="enabled">开启</option>
          <option value="disabled">关闭</option>
        </select>
      </>
    )
  }
  const formKey = ADVANCED_BOOLEAN_FORM_KEYS[spec.key as keyof typeof ADVANCED_BOOLEAN_FORM_KEYS]
  if (!formKey) return <p role="alert">字段缺少布尔映射</p>
  return (
    <label style={checkStyle}>
      <input
        type="checkbox"
        checked={form[formKey]}
        onChange={(event) => onChange(formKey, event.target.checked)}
      />
      <span>
        {spec.label}
        {current ? `（当前:${current[spec.key as keyof ProviderAccount] ? '开' : '关'}）` : ''}
      </span>
    </label>
  )
}

function renderDateTime(spec: AdvancedFieldSpec, { form, onChange, current }: RenderContext) {
  return (
    <>
      <FieldLabel spec={spec} current={current?.expires_at ?? null} />
      <NullableModeSelect value={form.expiresAtMode} onChange={(value) => onChange('expiresAtMode', value)} />
      {form.expiresAtMode === 'value' && (
        <input type="datetime-local" value={form.expiresAt} onChange={(event) => onChange('expiresAt', event.target.value)} style={inputStyle} />
      )}
    </>
  )
}

function renderErrorCodes(spec: AdvancedFieldSpec, { form, onChange }: RenderContext) {
  return (
    <>
      <FieldLabel spec={spec} />
      <input value={form.customErrorCodes} onChange={(event) => onChange('customErrorCodes', event.target.value)} placeholder="429, 529" style={inputStyle} />
    </>
  )
}

function renderRules(spec: AdvancedFieldSpec, ctx: RenderContext) {
  const { form, onChange, setRule, addRule } = ctx
  return (
    <>
      <FieldLabel spec={spec} />
      <select value={form.tempRulesMode} onChange={(event) => onChange('tempRulesMode', event.target.value as AccountAdvancedFormState['tempRulesMode'])} style={inputStyle}>
        <option value="unchanged">不设置 / 保持当前</option>
        <option value="replace">用下方列表整体替换</option>
      </select>
      {form.tempRulesMode === 'replace' && (
        <div style={ruleListStyle}>
          {form.tempUnschedulableRules.map((rule, index) => (
            <div key={index} style={ruleStyle}>
              <input aria-label={`规则 ${index + 1} 错误码`} value={rule.errorCode} onChange={(event) => setRule(index, 'errorCode', event.target.value)} placeholder="HTTP 错误码" style={inputStyle} />
              <input aria-label={`规则 ${index + 1} 时长`} value={rule.durationMinutes} onChange={(event) => setRule(index, 'durationMinutes', event.target.value)} placeholder="分钟" style={inputStyle} />
              <input aria-label={`规则 ${index + 1} 关键词`} value={rule.keywords} onChange={(event) => setRule(index, 'keywords', event.target.value)} placeholder="关键词，逗号分隔" style={inputStyle} />
              <input aria-label={`规则 ${index + 1} 说明`} value={rule.description} onChange={(event) => setRule(index, 'description', event.target.value)} placeholder="说明（可选）" style={inputStyle} />
              <button type="button" onClick={() => onChange('tempUnschedulableRules', form.tempUnschedulableRules.filter((_, ruleIndex) => ruleIndex !== index))} style={smallButtonStyle}>删除</button>
            </div>
          ))}
          {form.tempUnschedulableRules.length === 0 && <p style={helpStyle}>当前为空；保存后会清空全部规则。</p>}
          <button type="button" onClick={addRule} style={smallButtonStyle}>添加规则</button>
        </div>
      )}
    </>
  )
}

function renderProxy(spec: AdvancedFieldSpec, ctx: RenderContext) {
  const { form, onChange, proxies, proxyGroups, selectedGroup, groupListID, proxyLoading, proxyError } = ctx
  return (
    <>
      <FieldLabel spec={spec} />
      <select value={form.proxyMode} onChange={(event) => onChange('proxyMode', event.target.value as AccountAdvancedFormState['proxyMode'])} style={inputStyle}>
        <option value="direct">直连</option>
        <option value="proxy">指定单代理</option>
        <option value="group">指定代理组</option>
      </select>
      {form.proxyMode === 'proxy' && (
        <select value={form.proxyId} onChange={(event) => onChange('proxyId', event.target.value)} style={inputStyle}>
          <option value="">请选择代理…</option>
          {proxies.map((proxy) => <option key={proxy.id} value={proxy.id}>{proxy.name} · {proxy.status}</option>)}
        </select>
      )}
      {form.proxyMode === 'group' && (
        <>
          <input list={groupListID} value={form.proxyGroupId} onChange={(event) => onChange('proxyGroupId', event.target.value)} placeholder="代理组标识" style={inputStyle} />
          <datalist id={groupListID}>{proxyGroups.map((group) => <option key={group.groupId} value={group.groupId}>{group.active} active / {group.total} 总成员</option>)}</datalist>
          {proxyLoading && <p style={helpStyle}>正在核验 active 成员数…</p>}
          {!proxyLoading && !proxyError && <p style={helpStyle}>当前组 active:{selectedGroup.active} / 总成员:{selectedGroup.total}</p>}
          {!proxyLoading && !proxyError && selectedGroup.active === 0 && <p role="alert" style={dangerStyle}>该组没有 active 成员，绑定后请求会 fail-closed，不会直连。</p>}
        </>
      )}
      {proxyError && form.proxyMode !== 'direct' && <p role="alert" style={warningStyle}>无法核验代理可用性:{proxyError}</p>}
    </>
  )
}

function NullableModeSelect({ value, onChange }: { value: NullableFieldMode; onChange: (value: NullableFieldMode) => void }) {
  return (
    <select value={value} onChange={(event) => onChange(event.target.value as NullableFieldMode)} style={inputStyle}>
      <option value="unchanged">不设置 / 保持当前</option>
      <option value="value">设置为下方值</option>
      <option value="clear">清除为 null</option>
    </select>
  )
}

function FieldLabel({ spec, current }: { spec: AdvancedFieldSpec; current?: unknown }) {
  return <label style={labelStyle}>{spec.label}{current !== undefined ? `（当前:${current ?? 'null'}）` : ''}</label>
}

function summarizeProxyGroups(proxies: Proxy[]): ProxyGroupSummary[] {
  const groups = new Map<string, ProxyGroupSummary>()
  for (const proxy of proxies) {
    const groupID = proxy.group_id?.trim()
    if (!groupID) continue
    const summary = groups.get(groupID) ?? { groupId: groupID, total: 0, active: 0 }
    summary.total += 1
    if (proxy.status === 'active') summary.active += 1
    groups.set(groupID, summary)
  }
  return [...groups.values()].sort((left, right) => left.groupId.localeCompare(right.groupId))
}

const detailsStyle: CSSProperties = { border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', padding: 'var(--hk-space-3)' }
const summaryStyle: CSSProperties = { cursor: 'pointer', fontSize: 13, fontWeight: 650, color: 'var(--hk-ink-700)' }
const introStyle: CSSProperties = { margin: 'var(--hk-space-2) 0', fontSize: 11, lineHeight: 1.5, color: 'var(--hk-ink-500)' }
const gridStyle: CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 'var(--hk-space-3)' }
const fieldCardStyle: CSSProperties = { minWidth: 0, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)', padding: 'var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface-sunken)' }
const labelStyle: CSSProperties = { fontSize: 12, color: 'var(--hk-ink-700)', fontWeight: 600 }
const helpStyle: CSSProperties = { margin: 0, fontSize: 11, lineHeight: 1.5, color: 'var(--hk-ink-300)' }
const inputStyle: CSSProperties = { width: '100%', minWidth: 0, height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontSize: 12 }
const checkStyle: CSSProperties = { display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', fontSize: 12, color: 'var(--hk-ink-700)' }
const ruleListStyle: CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }
const ruleStyle: CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: 'var(--hk-space-2)' }
const smallButtonStyle: CSSProperties = { minHeight: 30, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', cursor: 'pointer' }
const dangerStyle: CSSProperties = { ...helpStyle, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', padding: 'var(--hk-space-2)', borderRadius: 'var(--hk-radius-sm)' }
const warningStyle: CSSProperties = { ...helpStyle, color: 'var(--hk-warn)' }
