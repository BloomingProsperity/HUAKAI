import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  bulkImportAliases,
  getTenantPolicy,
  listCapabilityBindings,
  setTenantPolicy,
  updateCapabilities,
  upsertCapabilityBinding,
} from './api'
import {
  CAPABILITY_GROUPS,
  buildCapabilitiesMap,
  mapAliasResultRows,
  mapAliasValidationRows,
  mapCapabilityBindingRows,
  parseAliasLines,
  splitParsedAliases,
  summarizeImportResults,
} from './modelregistry'
import type { AliasResultTableRow, AliasValidationTableRow, CapabilityBindingTableRow } from './modelregistry'
import type { AliasImportResult, CapabilityBinding, TenantPolicyView } from './types'
import { ModelAdminCard } from './ModelAdminCard'

/*
 * 模型注册(运维台 · admin 壳)。三块运维面,全部命中已存在的 /v1/admin/* 端点:
 *   1) 能力矩阵编辑      PUT /v1/admin/models/{id}/capabilities(任意能力 key→bool + 上限/模式)
 *   2) 能力绑定         GET/PUT /v1/admin/models/{id}/capability-bindings(白名单能力,per-scope)
 *   3) 映射批量导入      POST /v1/admin/models/aliases/bulk-import(逐行结果)
 *   4) 目录继承策略      GET/PUT /v1/admin/model-registry-policy?tenant_id(inherit_global_catalog 开关)
 * 模型用数字 DB id 定位(后端 path 契约);由顶部主体清单选择并回填，避免手工输入。
 */
export function ModelRegistryPage() {
  const [selectedModelId, setSelectedModelId] = useState<number | null>(null)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>模型注册</h1>
          <p className="hk-sub">
            模型主体 · 能力矩阵 · 能力绑定 · 映射批量导入 · 租户目录继承策略。列表选择会回填数字 DB id。
          </p>
        </div>
      </header>

      <ModelAdminCard selectedModelId={selectedModelId} onSelectModel={setSelectedModelId} />
      <CapabilityMatrixCard selectedModelId={selectedModelId} />
      <CapabilityBindingsCard selectedModelId={selectedModelId} />
      <AliasImportCard />
      <TenantPolicyCard />
    </div>
  )
}

// ── 块 1:能力矩阵编辑(整体替换 capabilities + 上限/模式) ──
function CapabilityMatrixCard({ selectedModelId }: { selectedModelId: number | null }) {
  const [modelId, setModelId] = useState('')
  const [toggles, setToggles] = useState<Record<string, boolean>>({})
  const [maxOut, setMaxOut] = useState('')
  const [mode, setMode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [okMsg, setOkMsg] = useState<string | null>(null)

  useEffect(() => {
    setModelId(selectedModelId == null ? '' : String(selectedModelId))
  }, [selectedModelId])

  const toggle = (cap: string) => setToggles((t) => ({ ...t, [cap]: !t[cap] }))

  const submit = async () => {
    const id = Number(modelId)
    if (!Number.isInteger(id) || id <= 0) {
      setError('请先从模型主体列表选择模型')
      return
    }
    setBusy(true)
    setError(null)
    setOkMsg(null)
    try {
      const body: { capabilities: Record<string, boolean>; max_output_tokens?: number | null; model_mode?: string | null } = {
        capabilities: buildCapabilitiesMap(toggles),
      }
      const mo = maxOut.trim()
      if (mo) body.max_output_tokens = Number(mo)
      const m = mode.trim()
      if (m) body.model_mode = m
      const resp = await updateCapabilities(id, body)
      const onKeys = Object.entries(resp.capabilities).filter(([, v]) => v).map(([k]) => k)
      setOkMsg(`已更新模型 #${resp.id}:开启 ${onKeys.length} 项能力${resp.mode ? `,模式 ${resp.mode}` : ''}。`)
    } catch (e) {
      setError(errText(e, '更新能力矩阵失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title="能力矩阵编辑" subtitle="勾选能力 → 整体替换该模型的 capabilities(未勾选即 false)。可选填最大输出 token 与模型模式。">
      <Row>
        <Field label="模型 id（列表回填）">
          <input value={modelId} readOnly placeholder="请从上方列表选择" style={inp} />
        </Field>
        <Field label="最大输出 token(可选)">
          <input value={maxOut} onChange={(e) => setMaxOut(e.target.value)} inputMode="numeric" placeholder="正整数" style={inp} />
        </Field>
        <Field label="模型模式(可选)">
          <input value={mode} onChange={(e) => setMode(e.target.value)} placeholder="如 reasoning" style={inp} />
        </Field>
      </Row>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', marginTop: 'var(--hk-space-3)' }}>
        {CAPABILITY_GROUPS.map((g) => (
          <div key={g.label}>
            <div style={{ fontSize: 12, color: 'var(--hk-ink-500)', marginBottom: 4 }}>{g.label}</div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-2)' }}>
              {g.items.map((cap) => (
                <button
                  key={cap}
                  type="button"
                  onClick={() => toggle(cap)}
                  style={toggles[cap] ? chipOn : chipOff}
                >
                  {cap}
                </button>
              ))}
            </div>
          </div>
        ))}
      </div>

      <Actions>
        <button type="button" disabled={busy} onClick={submit} className="hk-btn hk-btn--green">
          {busy ? '提交中…' : '提交能力矩阵'}
        </button>
      </Actions>
      {error && <ErrorBox>{error}</ErrorBox>}
      {okMsg && <OkBox>{okMsg}</OkBox>}
    </Card>
  )
}

// ── 块 2:能力绑定(per-scope 白名单能力,可读可 upsert) ──
function CapabilityBindingsCard({ selectedModelId }: { selectedModelId: number | null }) {
  const [modelId, setModelId] = useState('')
  const [bindings, setBindings] = useState<CapabilityBinding[] | null>(null)
  const [scope, setScope] = useState('tenant')
  const [tenantId, setTenantId] = useState('')
  const [capability, setCapability] = useState(CAPABILITY_GROUPS[0].items[0])
  const [enabled, setEnabled] = useState(true)
  const [capValue, setCapValue] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [okMsg, setOkMsg] = useState<string | null>(null)

  useEffect(() => {
    setModelId(selectedModelId == null ? '' : String(selectedModelId))
  }, [selectedModelId])

  const parsedModelId = (): number | null => {
    const id = Number(modelId)
    return Number.isInteger(id) && id > 0 ? id : null
  }

  const load = async () => {
    const id = parsedModelId()
    if (id == null) {
      setError('请先从模型主体列表选择模型')
      return
    }
    setBusy(true)
    setError(null)
    setOkMsg(null)
    try {
      const resp = await listCapabilityBindings(id)
      setBindings(resp.data)
    } catch (e) {
      setError(errText(e, '读取能力绑定失败'))
    } finally {
      setBusy(false)
    }
  }

  const upsert = async () => {
    const id = parsedModelId()
    if (id == null) {
      setError('请先从模型主体列表选择模型')
      return
    }
    const body: { scope: string; capability: string; enabled: boolean; tenant_id?: number; capability_value?: string } = {
      scope,
      capability,
      enabled,
    }
    if (scope === 'tenant') {
      const t = Number(tenantId)
      if (!Number.isInteger(t) || t <= 0) {
        setError('scope=tenant 时需正整数 tenant_id')
        return
      }
      body.tenant_id = t
    }
    const cv = capValue.trim()
    if (cv) body.capability_value = cv
    setBusy(true)
    setError(null)
    setOkMsg(null)
    try {
      const resp = await upsertCapabilityBinding(id, body)
      setOkMsg(`已 upsert 绑定:${resp.binding.capability}(${resp.binding.scope},${resp.binding.enabled ? '启用' : '停用'})。`)
      await load()
    } catch (e) {
      setError(errText(e, 'upsert 能力绑定失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title="能力绑定(白名单)" subtitle="per-(tenant|global) 的能力绑定;能力名取自后端白名单。来源(source)由服务端强制 operator。">
      <Row>
        <Field label="模型 id（列表回填）">
          <input value={modelId} readOnly placeholder="请从上方列表选择" style={inp} />
        </Field>
        <div style={{ display: 'flex', alignItems: 'flex-end' }}>
          <button type="button" disabled={busy} onClick={load} className="hk-btn">
            读取绑定
          </button>
        </div>
      </Row>

      {bindings != null && (
        <div style={{ marginTop: 'var(--hk-space-3)' }}>
          {bindings.length === 0 ? (
            <EmptyState title="该模型暂无能力绑定" />
          ) : (
            <DataListTable label="能力绑定列表" rows={mapCapabilityBindingRows(bindings)} rowKey={(row) => row.key} columns={capabilityBindingColumns} />
          )}
        </div>
      )}

      <div style={{ marginTop: 'var(--hk-space-4)', paddingTop: 'var(--hk-space-3)', borderTop: '1px solid var(--hk-line)' }}>
        <div style={{ fontSize: 12, color: 'var(--hk-ink-500)', marginBottom: 'var(--hk-space-2)' }}>新增 / 更新一条绑定</div>
        <Row>
          <Field label="scope">
            <select value={scope} onChange={(e) => setScope(e.target.value)} style={inp}>
              <option value="tenant">tenant</option>
              <option value="global">global</option>
            </select>
          </Field>
          {scope === 'tenant' && (
            <Field label="tenant_id">
              <input value={tenantId} onChange={(e) => setTenantId(e.target.value)} inputMode="numeric" style={inp} />
            </Field>
          )}
          <Field label="能力(白名单)">
            <select value={capability} onChange={(e) => setCapability(e.target.value)} style={inp}>
              {CAPABILITY_GROUPS.map((g) => (
                <optgroup key={g.label} label={g.label}>
                  {g.items.map((c) => <option key={c} value={c}>{c}</option>)}
                </optgroup>
              ))}
            </select>
          </Field>
          <Field label="能力值(可选)">
            <input value={capValue} onChange={(e) => setCapValue(e.target.value)} placeholder="如 high" style={inp} />
          </Field>
          <Field label="启用">
            <label style={{ display: 'flex', alignItems: 'center', gap: 6, height: 32, fontSize: 13 }}>
              <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
              enabled
            </label>
          </Field>
        </Row>
        <Actions>
          <button type="button" disabled={busy} onClick={upsert} className="hk-btn hk-btn--green">
            {busy ? '提交中…' : 'upsert 绑定'}
          </button>
        </Actions>
      </div>

      {error && <ErrorBox>{error}</ErrorBox>}
      {okMsg && <OkBox>{okMsg}</OkBox>}
    </Card>
  )
}

// ── 块 3:映射批量导入(逐行结果) ──
function AliasImportCard() {
  const [text, setText] = useState('')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [results, setResults] = useState<AliasImportResult[] | null>(null)
  const [localInvalid, setLocalInvalid] = useState<{ line: number; raw: string; error: string }[]>([])

  const submit = async () => {
    const parsed = parseAliasLines(text)
    const { rows, invalid } = splitParsedAliases(parsed)
    setLocalInvalid(invalid.map((p) => ({ line: p.line, raw: p.raw, error: p.error })))
    setResults(null)
    setError(null)
    if (rows.length === 0) {
      setError('没有可提交的合法映射行(请检查格式与逐行报错)。')
      return
    }
    setBusy(true)
    try {
      const body: { aliases: typeof rows; reason?: string } = { aliases: rows }
      const r = reason.trim()
      if (r) body.reason = r
      const resp = await bulkImportAliases(body)
      setResults(resp.results)
    } catch (e) {
      setError(errText(e, '映射批量导入失败'))
    } finally {
      setBusy(false)
    }
  }

  const summary = results ? summarizeImportResults(results) : null

  return (
    <Card title="映射批量导入" subtitle="逐行格式:model_id,映射名[,scope[,tenant_id[,display]]]。# 起首为注释。scope 缺省 tenant。">
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={6}
        placeholder={'# model_id,alias,scope,tenant_id,display\n42,claude-fast,tenant,9,极速版\n7,gpt-shared,global'}
        style={{ ...inp, height: 'auto', padding: 'var(--hk-space-3)', fontFamily: 'var(--hk-font-mono)', fontSize: 12, resize: 'vertical' }}
      />
      <Row>
        <Field label="原因备注(可选,落审计)">
          <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="如 季度映射重整" style={inp} />
        </Field>
      </Row>
      <Actions>
        <button type="button" disabled={busy} onClick={submit} className="hk-btn hk-btn--green">
          {busy ? '导入中…' : '批量导入'}
        </button>
      </Actions>

      {localInvalid.length > 0 && (
        <div style={{ marginTop: 'var(--hk-space-3)' }}>
          <div style={{ fontSize: 12, color: 'var(--hk-danger)', marginBottom: 4 }}>{localInvalid.length} 行本地校验未通过(未提交):</div>
          <DataListTable label="别名本地校验失败列表" rows={mapAliasValidationRows(localInvalid)} rowKey={(row) => row.line} columns={aliasValidationColumns} />
        </div>
      )}

      {results && (
        <div style={{ marginTop: 'var(--hk-space-3)' }}>
          {summary && (
            <div style={{ display: 'flex', gap: 'var(--hk-space-2)', marginBottom: 'var(--hk-space-2)' }}>
              <StatusBadge tone="ok">成功 {summary.upserted}</StatusBadge>
              <StatusBadge tone={summary.failed > 0 ? 'danger' : 'muted'}>失败 {summary.failed}</StatusBadge>
            </div>
          )}
          <DataListTable label="别名导入结果列表" rows={mapAliasResultRows(results)} rowKey={(row) => row.index} columns={aliasResultColumns} />
        </div>
      )}

      {error && <ErrorBox>{error}</ErrorBox>}
    </Card>
  )
}

// ── 块 4:租户目录继承策略(inherit_global_catalog 开关) ──
function TenantPolicyCard() {
  const [tenantId, setTenantId] = useState('')
  const [policy, setPolicy] = useState<TenantPolicyView | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [okMsg, setOkMsg] = useState<string | null>(null)

  const parsed = (): number | null => {
    const id = Number(tenantId)
    return Number.isInteger(id) && id > 0 ? id : null
  }

  const load = async () => {
    const id = parsed()
    if (id == null) {
      setError('请输入正整数 tenant_id')
      return
    }
    setBusy(true)
    setError(null)
    setOkMsg(null)
    try {
      const resp = await getTenantPolicy(id)
      setPolicy(resp.policy)
    } catch (e) {
      setError(errText(e, '读取目录策略失败'))
    } finally {
      setBusy(false)
    }
  }

  const flip = async (next: boolean) => {
    const id = parsed()
    if (id == null) {
      setError('请输入正整数 tenant_id')
      return
    }
    setBusy(true)
    setError(null)
    setOkMsg(null)
    try {
      const resp = await setTenantPolicy(id, { inherit_global_catalog: next })
      setPolicy(resp.policy)
      setOkMsg(`租户 #${resp.policy.tenant_id} 目录继承已设为 ${resp.policy.inherit_global_catalog ? '继承' : '不继承'}。`)
    } catch (e) {
      setError(errText(e, '设置目录策略失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title="租户目录继承策略" subtitle="inherit_global_catalog:租户是否继承全局模型目录(platform_admin only)。">
      <Row>
        <Field label="tenant_id(正整数)">
          <input value={tenantId} onChange={(e) => setTenantId(e.target.value)} inputMode="numeric" placeholder="如 9" style={inp} />
        </Field>
        <div style={{ display: 'flex', alignItems: 'flex-end' }}>
          <button type="button" disabled={busy} onClick={load} className="hk-btn">
            读取策略
          </button>
        </div>
      </Row>

      {policy && (
        <div style={{ marginTop: 'var(--hk-space-3)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 13 }}>
            <span style={{ color: 'var(--hk-ink-500)' }}>当前:</span>
            <StatusBadge tone={policy.inherit_global_catalog ? 'ok' : 'muted'}>
              {policy.inherit_global_catalog ? '继承全局目录' : '不继承'}
            </StatusBadge>
            {policy.updated_by_actor && <span style={{ color: 'var(--hk-ink-300)', fontSize: 11 }}>由 {policy.updated_by_actor}</span>}
          </div>
          <Actions>
            <button type="button" disabled={busy || policy.inherit_global_catalog} onClick={() => flip(true)} className="hk-btn hk-btn--green">
              开启继承
            </button>
            <button type="button" disabled={busy || !policy.inherit_global_catalog} onClick={() => flip(false)} className="hk-btn">
              关闭继承
            </button>
          </Actions>
        </div>
      )}

      {error && <ErrorBox>{error}</ErrorBox>}
      {okMsg && <OkBox>{okMsg}</OkBox>}
    </Card>
  )
}

// ── 错误归一化(不打印任何内容到 console) ──
function errText(e: unknown, fallback: string): string {
  return e instanceof ApiError ? `${e.message}(${e.code})` : fallback
}

// ── 通用布局原子 ──
function Card({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) {
  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>{title}</h3>
      </div>
      <div className="hk-card__body" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
        <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', margin: '0 0 var(--hk-space-1)' }}>{subtitle}</p>
        {children}
      </div>
    </section>
  )
}
function Row({ children }: { children: React.ReactNode }) {
  return <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)', alignItems: 'flex-end' }}>{children}</div>
}
function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)', minWidth: 140 }}>
      {label}
      {children}
    </label>
  )
}
function Actions({ children }: { children: React.ReactNode }) {
  return <div style={{ display: 'flex', gap: 'var(--hk-space-2)', marginTop: 'var(--hk-space-3)' }}>{children}</div>
}
function ErrorBox({ children }: { children: React.ReactNode }) {
  return <div style={{ marginTop: 'var(--hk-space-3)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{children}</div>
}
function OkBox({ children }: { children: React.ReactNode }) {
  return <div style={{ marginTop: 'var(--hk-space-3)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }}>{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const chipOff: React.CSSProperties = { padding: '3px 10px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-pill)', background: 'var(--hk-surface)', color: 'var(--hk-ink-500)', fontSize: 12, cursor: 'pointer' }
const chipOn: React.CSSProperties = { padding: '3px 10px', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-pill)', background: 'var(--hk-primary-50)', color: 'var(--hk-primary-600)', fontSize: 12, fontWeight: 600, cursor: 'pointer' }

const capabilityBindingColumns: DataListColumn<CapabilityBindingTableRow>[] = [
  { key: 'capability', label: '能力', render: (row) => <span className="hk-mono">{row.capability}</span> },
  { key: 'scope', label: 'scope', render: (row) => row.scope },
  { key: 'tenant', label: '租户', render: (row) => row.tenant },
  { key: 'value', label: '值', render: (row) => <span className="hk-mono">{row.value}</span> },
  { key: 'enabled', label: '启用', badge: true, render: (row) => <StatusBadge tone={row.enabledTone}>{row.enabled}</StatusBadge> },
  { key: 'source', label: '来源', render: (row) => row.source },
]

const aliasValidationColumns: DataListColumn<AliasValidationTableRow>[] = [
  { key: 'line', label: '行', render: (row) => row.line },
  { key: 'raw', label: '内容', render: (row) => <span className="hk-mono">{row.raw}</span> },
  { key: 'error', label: '错误', render: (row) => <span style={{ color: 'var(--hk-danger)' }}>{row.error}</span> },
]

const aliasResultColumns: DataListColumn<AliasResultTableRow>[] = [
  { key: 'index', label: '#', render: (row) => row.index },
  { key: 'alias', label: '映射名', render: (row) => <span className="hk-mono">{row.alias}</span> },
  { key: 'model-id', label: '模型 id', render: (row) => row.modelId },
  { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
  { key: 'error', label: '错误', render: (row) => <span style={{ color: row.hasError ? 'var(--hk-danger)' : 'var(--hk-ink-500)' }}>{row.error}</span> },
]
