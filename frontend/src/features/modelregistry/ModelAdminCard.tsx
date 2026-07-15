import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { createAdminModel, deleteAdminModel, listAdminModels, updateAdminModel } from './api'
import { mapAdminModelRows, type AdminModelTableRow } from './modelregistry'
import type { AdminModel, AdminModelCreateRequest, AdminModelScope, AdminModelStatus } from './types'

interface Props {
  selectedModelId: number | null
  onSelectModel: (id: number | null) => void
}

interface ModelDraft {
  canonicalId: string
  protocolFamily: string
  providerModelId: string
  contextWindow: string
  requestTimeoutMS: string
  pricingClass: string
  modelOwner: string
  status: AdminModelStatus
}

const emptyDraft: ModelDraft = {
  canonicalId: '',
  protocolFamily: 'openai_chat',
  providerModelId: '',
  contextWindow: '0',
  requestTimeoutMS: '60000',
  pricingClass: 'standard',
  modelOwner: 'HUAKAI',
  status: 'active',
}

/** 模型主体列表与写操作卡。选择行后把数字 id 回填给页面内其余模型运维卡。 */
export function ModelAdminCard({ selectedModelId, onSelectModel }: Props) {
  const [scope, setScope] = useState<AdminModelScope>('tenant')
  const [tenantId, setTenantId] = useState('')
  const [models, setModels] = useState<AdminModel[] | null>(null)
  const [draft, setDraft] = useState<ModelDraft>(emptyDraft)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [okMessage, setOkMessage] = useState<string | null>(null)

  const selected = models?.find((model) => model.id === selectedModelId) ?? null

  const parseTenant = (): { ok: true; value?: number } | { ok: false } => {
    const raw = tenantId.trim()
    if (scope === 'global') {
      if (raw !== '') {
        setError('scope=global 时 tenant_id 必须留空')
        return { ok: false }
      }
      return { ok: true }
    }
    if (raw === '') return { ok: true }
    const value = Number(raw)
    if (!Number.isInteger(value) || value <= 0) {
      setError('tenant_id 必须是正整数；tenant_operator 可留空使用自身 scope')
      return { ok: false }
    }
    return { ok: true, value }
  }

  const parseDraft = (): AdminModelCreateRequest | null => {
    const canonicalId = draft.canonicalId.trim()
    const protocolFamily = draft.protocolFamily.trim()
    const providerModelId = draft.providerModelId.trim()
    const pricingClass = draft.pricingClass.trim()
    const modelOwner = draft.modelOwner.trim()
    const contextWindow = Number(draft.contextWindow)
    const requestTimeoutMS = Number(draft.requestTimeoutMS)
    if (!canonicalId || !protocolFamily || !providerModelId || !pricingClass || !modelOwner) {
      setError('canonical_id、协议族、上游模型 id、计价类与模型归属不能为空')
      return null
    }
    if (!Number.isInteger(contextWindow) || contextWindow < 0) {
      setError('上下文窗口必须是非负整数')
      return null
    }
    if (!Number.isInteger(requestTimeoutMS) || requestTimeoutMS <= 0) {
      setError('请求超时必须是正整数毫秒')
      return null
    }
    return {
      canonical_id: canonicalId,
      protocol_family: protocolFamily,
      default_provider_model_id: providerModelId,
      default_context_window: contextWindow,
      default_request_timeout_ms: requestTimeoutMS,
      pricing_class: pricingClass,
      model_owner: modelOwner,
      status: draft.status,
    }
  }

  const load = async () => {
    const tenant = parseTenant()
    if (!tenant.ok) return
    setBusy(true)
    setError(null)
    setOkMessage(null)
    try {
      const response = await listAdminModels(scope, tenant.value)
      setModels(response.items)
      if (selectedModelId != null && !response.items.some((model) => model.id === selectedModelId)) {
        onSelectModel(null)
      }
    } catch (reason) {
      setError(errorText(reason, '读取模型主体失败'))
    } finally {
      setBusy(false)
    }
  }

  const selectModel = (id: number) => {
    const model = models?.find((item) => item.id === id)
    if (!model) return
    onSelectModel(model.id)
    setDraft({
      canonicalId: model.canonical_id,
      protocolFamily: model.protocol_family,
      providerModelId: model.default_provider_model_id,
      contextWindow: String(model.default_context_window),
      requestTimeoutMS: String(model.default_request_timeout_ms),
      pricingClass: model.pricing_class,
      modelOwner: model.model_owner,
      status: model.status,
    })
    setError(null)
    setOkMessage(`已选择模型 #${model.id}；数字 id 已回填到下方能力卡。`)
  }

  const replaceModel = (model: AdminModel) => {
    setModels((current) => {
      const next = (current ?? []).filter((item) => item.id !== model.id)
      next.push(model)
      return next.sort((left, right) => left.canonical_id.localeCompare(right.canonical_id) || left.id - right.id)
    })
    selectModelFromValue(model)
  }

  const selectModelFromValue = (model: AdminModel) => {
    onSelectModel(model.id)
    setDraft({
      canonicalId: model.canonical_id,
      protocolFamily: model.protocol_family,
      providerModelId: model.default_provider_model_id,
      contextWindow: String(model.default_context_window),
      requestTimeoutMS: String(model.default_request_timeout_ms),
      pricingClass: model.pricing_class,
      modelOwner: model.model_owner,
      status: model.status,
    })
  }

  const create = async () => {
    const tenant = parseTenant()
    const body = parseDraft()
    if (!tenant.ok || body == null) return
    setBusy(true)
    setError(null)
    setOkMessage(null)
    try {
      const created = await createAdminModel(scope, tenant.value, body)
      replaceModel(created)
      setOkMessage(`已创建模型 #${created.id}，并回填数字 id。`)
    } catch (reason) {
      setError(errorText(reason, '创建模型主体失败'))
    } finally {
      setBusy(false)
    }
  }

  const save = async () => {
    if (!selected) {
      setError('请先从列表选择要编辑的模型')
      return
    }
    const tenant = parseTenant()
    const body = parseDraft()
    if (!tenant.ok || body == null) return
    setBusy(true)
    setError(null)
    setOkMessage(null)
    try {
      const updated = await updateAdminModel(selected.id, scope, tenant.value, {
        protocol_family: body.protocol_family,
        default_provider_model_id: body.default_provider_model_id,
        default_context_window: body.default_context_window,
        default_request_timeout_ms: body.default_request_timeout_ms,
        pricing_class: body.pricing_class,
        model_owner: body.model_owner,
      })
      replaceModel(updated)
      setOkMessage(`已更新模型 #${updated.id}。canonical_id 作为稳定身份保持不变。`)
    } catch (reason) {
      setError(errorText(reason, '更新模型主体失败'))
    } finally {
      setBusy(false)
    }
  }

  const toggleStatus = async () => {
    if (!selected) {
      setError('请先从列表选择要停用或启用的模型')
      return
    }
    const tenant = parseTenant()
    if (!tenant.ok) return
    const next: AdminModelStatus = selected.status === 'active' ? 'disabled' : 'active'
    setBusy(true)
    setError(null)
    setOkMessage(null)
    try {
      const updated = await updateAdminModel(selected.id, scope, tenant.value, { status: next })
      replaceModel(updated)
      setOkMessage(`模型 #${updated.id} 已${next === 'active' ? '启用' : '停用'}。`)
    } catch (reason) {
      setError(errorText(reason, '切换模型状态失败'))
    } finally {
      setBusy(false)
    }
  }

  const remove = async () => {
    if (!selected) {
      setError('请先从列表选择要软删的模型')
      return
    }
    if (!window.confirm(`确认软删模型 #${selected.id}（${selected.canonical_id}）？`)) return
    const tenant = parseTenant()
    if (!tenant.ok) return
    setBusy(true)
    setError(null)
    setOkMessage(null)
    try {
      await deleteAdminModel(selected.id, scope, tenant.value)
      setModels((current) => current?.filter((model) => model.id !== selected.id) ?? [])
      onSelectModel(null)
      setDraft(emptyDraft)
      setOkMessage(`模型 #${selected.id} 已软删。`)
    } catch (reason) {
      setError(errorText(reason, '软删模型主体失败'))
    } finally {
      setBusy(false)
    }
  }

  const rows = mapAdminModelRows(models ?? [])
  return (
    <section className="hk-card">
      <div className="hk-card__head"><h3>模型主体列表</h3></div>
      <div className="hk-card__body" style={bodyStyle}>
        <p style={hintStyle}>带数字 DB id 的运维清单。tenant_operator 可留空 tenant_id 使用自身 scope；platform_admin 管 tenant 时需显式填写。</p>
        <div style={rowStyle}>
          <Field label="scope">
            <select value={scope} onChange={(event) => { const next = event.target.value as AdminModelScope; setScope(next); if (next === 'global') setTenantId('') }} style={inputStyle}>
              <option value="tenant">tenant</option>
              <option value="global">global</option>
            </select>
          </Field>
          <Field label="tenant_id（tenant 可选）">
            <input value={tenantId} onChange={(event) => setTenantId(event.target.value)} disabled={scope === 'global'} inputMode="numeric" style={inputStyle} />
          </Field>
          <button type="button" disabled={busy} onClick={load} className="hk-btn">{busy ? '处理中…' : '刷新列表'}</button>
        </div>

        {models != null && (rows.length === 0
          ? <EmptyState title="当前 scope 暂无模型主体" />
          : <DataListTable
              label="模型主体列表"
              rows={rows}
              rowKey={(row) => row.id}
              columns={modelColumns}
              actions={[{
                label: (row) => row.id === selectedModelId ? '已选择' : '选择并回填',
                disabled: (row) => row.id === selectedModelId,
                onClick: (row) => selectModel(row.id),
              }]}
            />)}

        <div style={editorStyle}>
          <div style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
            {selected ? `编辑模型 #${selected.id}（canonical_id 不可变）` : '填写后新建模型；选择列表行后可编辑、停用或软删'}
          </div>
          <div style={rowStyle}>
            <Field label="canonical_id">
              <input value={draft.canonicalId} onChange={(event) => setDraft({ ...draft, canonicalId: event.target.value })} disabled={selected != null} style={inputStyle} />
            </Field>
            <Field label="protocol_family">
              <select value={draft.protocolFamily} onChange={(event) => setDraft({ ...draft, protocolFamily: event.target.value })} style={inputStyle}>
                <option value="openai_chat">openai_chat</option>
                <option value="openai_responses">openai_responses</option>
                <option value="anthropic_messages">anthropic_messages</option>
                <option value="gemini">gemini</option>
              </select>
            </Field>
            <Field label="上游模型 id">
              <input value={draft.providerModelId} onChange={(event) => setDraft({ ...draft, providerModelId: event.target.value })} style={inputStyle} />
            </Field>
            <Field label="上下文窗口">
              <input value={draft.contextWindow} onChange={(event) => setDraft({ ...draft, contextWindow: event.target.value })} inputMode="numeric" style={inputStyle} />
            </Field>
            <Field label="请求超时（毫秒）">
              <input value={draft.requestTimeoutMS} onChange={(event) => setDraft({ ...draft, requestTimeoutMS: event.target.value })} inputMode="numeric" style={inputStyle} />
            </Field>
            <Field label="pricing_class">
              <input value={draft.pricingClass} onChange={(event) => setDraft({ ...draft, pricingClass: event.target.value })} style={inputStyle} />
            </Field>
            <Field label="model_owner">
              <input value={draft.modelOwner} onChange={(event) => setDraft({ ...draft, modelOwner: event.target.value })} style={inputStyle} />
            </Field>
          </div>
          <div style={actionsStyle}>
            <button type="button" disabled={busy || selected == null} onClick={() => { onSelectModel(null); setDraft(emptyDraft); setError(null); setOkMessage('已切换到新建模式。') }} className="hk-btn">准备新建</button>
            <button type="button" disabled={busy || selected != null} onClick={create} className="hk-btn hk-btn--green">新建模型</button>
            <button type="button" disabled={busy || selected == null} onClick={save} className="hk-btn hk-btn--green">保存编辑</button>
            <button type="button" disabled={busy || selected == null} onClick={toggleStatus} className="hk-btn">
              {selected?.status === 'active' ? '停用' : '启用'}
            </button>
            <button type="button" disabled={busy || selected == null} onClick={remove} className="hk-btn hk-btn--danger">软删</button>
            {selected && <StatusBadge tone={selected.status === 'active' ? 'ok' : 'muted'}>{selected.status === 'active' ? '启用' : '停用'}</StatusBadge>}
          </div>
        </div>

        {error && <div style={errorStyle}>{error}</div>}
        {okMessage && <div style={okStyle}>{okMessage}</div>}
      </div>
    </section>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return <label style={fieldStyle}>{label}{children}</label>
}

function errorText(reason: unknown, fallback: string): string {
  return reason instanceof ApiError ? `${reason.message}（${reason.code}）` : fallback
}

const modelColumns: DataListColumn<AdminModelTableRow>[] = [
  { key: 'id', label: '数字 id', render: (row) => <span className="hk-mono">#{row.id}</span> },
  { key: 'canonical', label: 'canonical_id', render: (row) => <span className="hk-mono">{row.canonicalId}</span> },
  { key: 'provider', label: '上游模型', render: (row) => <span className="hk-mono">{row.providerModelId}</span> },
  { key: 'protocol', label: '协议族', render: (row) => row.protocolFamily },
  { key: 'scope', label: 'scope / tenant', render: (row) => `${row.scope} / ${row.tenant}` },
  { key: 'context', label: '上下文', render: (row) => row.contextWindow.toLocaleString() },
  { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
]

const bodyStyle: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }
const hintStyle: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }
const rowStyle: React.CSSProperties = { display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)', alignItems: 'flex-end' }
const editorStyle: React.CSSProperties = { borderTop: '1px solid var(--hk-line)', paddingTop: 'var(--hk-space-3)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }
const fieldStyle: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)', minWidth: 150 }
const inputStyle: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const actionsStyle: React.CSSProperties = { display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 'var(--hk-space-2)' }
const errorStyle: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const okStyle: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
