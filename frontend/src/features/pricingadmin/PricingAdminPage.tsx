import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  deleteCacheOverride,
  deletePricingRatio,
  getBillingSettings,
  listCacheOverrides,
  listPricingRatios,
  setCacheOverride,
  updateBillingSettings,
  upsertPricingRatio,
  verifyRatioAudit,
} from './api'
import {
  billingPolicyLabel,
  buildBillingSettingsUpdate,
  CACHE_SCOPES,
  scopeLabel,
  TOOL_SURCHARGE_DEFAULTS,
  validateCacheQualifier,
  validateMultiplier,
  validateRatio,
  validateTenantId,
  type CacheOverrideQualifier,
} from './pricingadmin'
import type {
  BillingSettingsResponse,
  CacheOverride,
  CacheOverrideScope,
  PricingRatio,
  RatioAuditVerifyResponse,
} from './types'

/*
 * 模型定价设置(运维台,admin 壳)。三块:
 *  1. 分组倍率 /admin/v1/pricing/ratios —— 按租户列出 + 编辑(写动作 money-gated:仅 platform_admin)
 *  2. 缓存价覆盖 /v1/admin/cache-price-overrides —— 列出 + 设置/清除(money-gated)
 *  3. 工具附加费 —— 无 admin 写端点,只读展示默认价(env HUAKAI_TOOL_SURCHARGE_ENABLED 控开关)
 *
 * 写表单的提交按钮统一标注「需 Owner 确认」:这些动作直接改计费倍率,影响真金,谨慎执行。
 */
export function PricingAdminPage() {
  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>模型定价设置</h1>
          <p className="hk-sub">
            分组倍率 · 缓存价覆盖 · 计费策略 · 工具附加费。写动作直接影响计费,提交前请确认。
          </p>
        </div>
      </header>
      <RatioSection />
      <CacheOverrideSection />
      <BillingPolicySection />
      <ToolSurchargeSection />
    </div>
  )
}

// ============ 计费策略 ============

function BillingPolicySection() {
  const [draftTenant, setDraftTenant] = useState('1')
  const [tenantId, setTenantId] = useState<number | null>(null)
  const [settings, setSettings] = useState<BillingSettingsResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [editOpen, setEditOpen] = useState(false)

  useEffect(() => {
    if (tenantId === null) return
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    getBillingSettings(tenantId, ctrl.signal)
      .then((resp) => setSettings(resp))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载计费策略失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [tenantId, refreshNonce])

  const query = () => {
    const v = validateTenantId(draftTenant)
    if ('error' in v) {
      setError(v.error)
      return
    }
    setNotice(null)
    setError(null)
    setTenantId(v.value)
  }

  return (
    <Section
      title="计费策略"
      subtitle="流式仅输入后中断场景的结算策略(stream_input_only_interrupted_policy)。改动直接影响计费,需 Owner 确认。"
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          query()
        }}
        style={toolbar}
      >
        <label style={fieldInline}>
          租户 id
          <input value={draftTenant} onChange={(e) => setDraftTenant(e.target.value)} style={inp} placeholder="如 1" />
        </label>
        <button type="submit" className="hk-btn hk-btn--green">查询</button>
        {settings && (
          <button type="button" onClick={() => setEditOpen(true)} className="hk-btn">修改策略</button>
        )}
      </form>

      {error && <ErrorBox>{error}</ErrorBox>}
      {notice && (
        <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#235a82', background: '#e8f1f8', border: '1px solid #cfe0ee' }}>
          {notice}
        </div>
      )}

      {editOpen && settings && tenantId !== null && (
        <BillingPolicyModal
          tenantId={tenantId}
          current={settings}
          onClose={() => setEditOpen(false)}
          onSaved={(updated) => {
            setEditOpen(false)
            setSettings(updated)
            setNotice('计费策略已更新')
            setRefreshNonce((n) => n + 1)
          }}
        />
      )}

      {tenantId === null ? (
        <Empty>输入租户 id 后查询当前计费策略。</Empty>
      ) : (
        <Card>
          {loading && !settings ? (
            <Empty>加载中…</Empty>
          ) : settings ? (
            <Table head={['策略键', '当前值', '来源', '更新人', '更新时间']}>
              <tr>
                <td className="hk-mono">{settings.key}</td>
                <td>
                  <StatusBadge tone="info">{billingPolicyLabel(settings.value)}</StatusBadge>
                  <span style={{ marginLeft: 6, fontFamily: 'var(--hk-font-mono)', fontSize: 12, color: 'var(--hk-ink-500)' }}>
                    ({settings.value})
                  </span>
                </td>
                <td>
                  <StatusBadge tone={settings.source === 'tenant' ? 'ok' : 'muted'}>
                    {settings.source === 'tenant' ? '租户自定义' : '全局默认'}
                  </StatusBadge>
                </td>
                <td>{settings.updated_by || '—'}</td>
                <td>{fmt(settings.updated_at ?? undefined)}</td>
              </tr>
            </Table>
          ) : (
            <Empty>无数据。</Empty>
          )}
        </Card>
      )}
    </Section>
  )
}

function BillingPolicyModal({
  tenantId,
  current,
  onClose,
  onSaved,
}: {
  tenantId: number
  current: BillingSettingsResponse
  onClose: () => void
  onSaved: (updated: BillingSettingsResponse) => void
}) {
  const [policy, setPolicy] = useState(current.value)
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    const built = buildBillingSettingsUpdate(tenantId, policy, reason, current.allowed_values)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setBusy(true)
    setError(null)
    try {
      const updated = await updateBillingSettings(built.request)
      onSaved(updated)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal onClose={onClose} title="修改计费策略">
      <Field label="策略值">
        <select value={policy} onChange={(e) => setPolicy(e.target.value)} style={inp}>
          {current.allowed_values.map((v) => (
            <option key={v} value={v}>{billingPolicyLabel(v)}({v})</option>
          ))}
        </select>
      </Field>
      {current.roadmap_values.length > 0 && (
        <div style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
          路线图值(暂不可启用):{current.roadmap_values.join('、')}
        </div>
      )}
      <Field label="变更原因(必填,写入审计)">
        <input value={reason} onChange={(e) => setReason(e.target.value)} style={inp} placeholder="如:合规要求 / 业务调整" />
      </Field>
      <GatedHint />
      {error && <ErrorBox>{error}</ErrorBox>}
      <ModalActions onCancel={onClose} onConfirm={submit} busy={busy} />
    </Modal>
  )
}

// ============ 分组倍率 ============

function RatioSection() {
  const [draftTenant, setDraftTenant] = useState('1')
  const [tenantId, setTenantId] = useState<number | null>(null)
  const [rows, setRows] = useState<PricingRatio[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [editing, setEditing] = useState<PricingRatio | 'new' | null>(null)
  const [audit, setAudit] = useState<RatioAuditVerifyResponse | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const refresh = () => setRefreshNonce((n) => n + 1)

  useEffect(() => {
    if (tenantId === null) return
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    listPricingRatios(tenantId, 0, 100, ctrl.signal)
      .then((resp) => setRows(resp.items))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载分组倍率失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [tenantId, refreshNonce])

  const query = () => {
    const v = validateTenantId(draftTenant)
    if ('error' in v) {
      setError(v.error)
      return
    }
    setAudit(null)
    setTenantId(v.value)
  }

  const runAudit = async () => {
    if (tenantId === null) return
    setError(null)
    try {
      setAudit(await verifyRatioAudit(tenantId))
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '审计校验失败')
    }
  }

  const remove = async (row: PricingRatio) => {
    if (tenantId === null) return
    setBusyId(row.id)
    setError(null)
    try {
      await deletePricingRatio(tenantId, row.pool_group_id)
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '删除失败')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <Section
      title="分组倍率"
      subtitle="按账号池分组对该租户的计费倍率(public_ratio 决定是否对外暴露)。范围 0.01–100。"
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          query()
        }}
        style={toolbar}
      >
        <label style={fieldInline}>
          租户 id
          <input value={draftTenant} onChange={(e) => setDraftTenant(e.target.value)} style={inp} placeholder="如 1" />
        </label>
        <button type="submit" className="hk-btn hk-btn--green">查询</button>
        {tenantId !== null && (
          <>
            <button type="button" onClick={() => setEditing('new')} className="hk-btn">＋ 新增倍率</button>
            <button type="button" onClick={runAudit} className="hk-btn">审计校验</button>
          </>
        )}
      </form>

      {audit && (
        <div style={{ fontSize: 13 }}>
          审计链:{' '}
          <StatusBadge tone={audit.ok ? 'ok' : 'danger'}>{audit.ok ? '完整' : '异常'}</StatusBadge>
          {!audit.ok && audit.reason && <span style={{ marginLeft: 8, color: 'var(--hk-ink-500)' }}>{audit.reason}</span>}
        </div>
      )}

      {error && <ErrorBox>{error}</ErrorBox>}

      {editing && tenantId !== null && (
        <RatioEditModal
          tenantId={tenantId}
          row={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            refresh()
          }}
        />
      )}

      {tenantId === null ? (
        <Empty>输入租户 id 后查询。</Empty>
      ) : (
        <Card>
          {loading && rows.length === 0 ? (
            <Empty>加载中…</Empty>
          ) : rows.length === 0 ? (
            <Empty>该租户暂无自定义倍率(走默认)。</Empty>
          ) : (
            <Table head={['分组 id', '倍率', '对外暴露', '更新人', '更新时间', '']}>
              {rows.map((r) => (
                <tr key={r.id}>
                  <td className="hk-mono">{r.pool_group_id}</td>
                  <td className="hk-mono" style={{ textAlign: 'right' }}>{r.public_ratio ? r.ratio ?? '—' : '(隐藏)'}</td>
                  <td>
                    <StatusBadge tone={r.public_ratio ? 'info' : 'muted'}>{r.public_ratio ? '是' : '否'}</StatusBadge>
                  </td>
                  <td>{r.updated_by || '—'}</td>
                  <td>{fmt(r.updated_at)}</td>
                  <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                    <button type="button" onClick={() => setEditing(r)} className="hk-btn hk-btn--sm">编辑</button>
                    <button type="button" disabled={busyId === r.id} onClick={() => remove(r)} className="hk-btn hk-btn--sm hk-btn--danger" style={{ marginLeft: 'var(--hk-space-2)' }}>删除</button>
                  </td>
                </tr>
              ))}
            </Table>
          )}
        </Card>
      )}
    </Section>
  )
}

function RatioEditModal({
  tenantId,
  row,
  onClose,
  onSaved,
}: {
  tenantId: number
  row: PricingRatio | null
  onClose: () => void
  onSaved: () => void
}) {
  const [poolGroupId, setPoolGroupId] = useState(row ? String(row.pool_group_id) : '')
  const [ratio, setRatio] = useState(row?.ratio ?? '')
  const [publicRatio, setPublicRatio] = useState(row?.public_ratio ?? false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    const pg = validateTenantId(poolGroupId) // 复用正整数校验(pool_group_id 同样要求正整数)
    if ('error' in pg) {
      setError('分组 id 必须是正整数')
      return
    }
    const rv = validateRatio(ratio)
    if ('error' in rv) {
      setError(rv.error)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await upsertPricingRatio(tenantId, pg.value, { ratio: rv.value, public_ratio: publicRatio })
      onSaved()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal onClose={onClose} title={row ? `编辑分组 ${row.pool_group_id} 倍率` : '新增分组倍率'}>
      <Field label="分组 id(pool_group_id)">
        <input
          value={poolGroupId}
          onChange={(e) => setPoolGroupId(e.target.value)}
          disabled={!!row}
          style={inp}
          placeholder="正整数"
        />
      </Field>
      <Field label="倍率(0.01–100)">
        <input value={ratio} onChange={(e) => setRatio(e.target.value)} style={inp} placeholder="如 1.5" />
      </Field>
      <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--hk-ink-700)' }}>
        <input type="checkbox" checked={publicRatio} onChange={(e) => setPublicRatio(e.target.checked)} />
        对外暴露倍率(public_ratio)
      </label>
      <GatedHint />
      {error && <ErrorBox>{error}</ErrorBox>}
      <ModalActions onCancel={onClose} onConfirm={submit} busy={busy} />
    </Modal>
  )
}

// ============ 缓存价覆盖 ============

function CacheOverrideSection() {
  const [rows, setRows] = useState<CacheOverride[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [editOpen, setEditOpen] = useState(false)
  const [busyKey, setBusyKey] = useState<string | null>(null)

  const refresh = () => setRefreshNonce((n) => n + 1)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    listCacheOverrides(ctrl.signal)
      .then((resp) => setRows(resp.overrides))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载缓存价覆盖失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [refreshNonce])

  const remove = async (r: CacheOverride) => {
    const key = rowKey(r)
    setBusyKey(key)
    setError(null)
    try {
      await deleteCacheOverride(r.scope as CacheOverrideScope, {
        model: r.model,
        tenantId: r.tenant_id,
      })
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '清除失败')
    } finally {
      setBusyKey(null)
    }
  }

  return (
    <Section title="缓存价覆盖" subtitle="按 global / model / tenant 覆盖缓存命中计费倍率;未列出的走官方价。">
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
        <button type="button" onClick={() => setEditOpen(true)} className="hk-btn">＋ 设置覆盖</button>
      </div>

      {error && <ErrorBox>{error}</ErrorBox>}

      {editOpen && (
        <CacheOverrideModal
          onClose={() => setEditOpen(false)}
          onSaved={() => {
            setEditOpen(false)
            refresh()
          }}
        />
      )}

      <Card>
        {loading && rows.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : rows.length === 0 ? (
          <Empty>无覆盖(全部走官方价)。</Empty>
        ) : (
          <Table head={['范围', '限定', '倍率', '更新时间', '']}>
            {rows.map((r) => (
              <tr key={rowKey(r)}>
                <td>
                  <StatusBadge tone="info">{scopeLabel(r.scope)}</StatusBadge>
                </td>
                <td>{r.model ? r.model : r.tenant_id ? `租户 ${r.tenant_id}` : '—'}</td>
                <td className="hk-mono" style={{ textAlign: 'right' }}>{r.multiplier}</td>
                <td>{fmt(r.updated_at)}</td>
                <td style={{ textAlign: 'right' }}>
                  <button type="button" disabled={busyKey === rowKey(r)} onClick={() => remove(r)} className="hk-btn hk-btn--sm hk-btn--danger">清除</button>
                </td>
              </tr>
            ))}
          </Table>
        )}
      </Card>
    </Section>
  )
}

function CacheOverrideModal({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [scope, setScope] = useState<CacheOverrideScope>('global')
  const [model, setModel] = useState('')
  const [tenantId, setTenantId] = useState('')
  const [multiplier, setMultiplier] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    const q: CacheOverrideQualifier = { model, tenantId }
    const qual = validateCacheQualifier(scope, q)
    if ('error' in qual) {
      setError(qual.error)
      return
    }
    const mv = validateMultiplier(multiplier)
    if ('error' in mv) {
      setError(mv.error)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await setCacheOverride(scope, { multiplier: mv.value }, qual)
      onSaved()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal onClose={onClose} title="设置缓存价覆盖">
      <Field label="范围">
        <select value={scope} onChange={(e) => setScope(e.target.value as CacheOverrideScope)} style={inp}>
          {CACHE_SCOPES.map((s) => (
            <option key={s.value} value={s.value}>{s.label}</option>
          ))}
        </select>
      </Field>
      {scope === 'model' && (
        <Field label="模型名">
          <input value={model} onChange={(e) => setModel(e.target.value)} style={inp} placeholder="如 claude-3-5-sonnet" />
        </Field>
      )}
      {scope === 'tenant' && (
        <Field label="租户 id">
          <input value={tenantId} onChange={(e) => setTenantId(e.target.value)} style={inp} placeholder="正整数" />
        </Field>
      )}
      <Field label="倍率(正小数)">
        <input value={multiplier} onChange={(e) => setMultiplier(e.target.value)} style={inp} placeholder="如 0.5" />
      </Field>
      <GatedHint />
      {error && <ErrorBox>{error}</ErrorBox>}
      <ModalActions onCancel={onClose} onConfirm={submit} busy={busy} />
    </Modal>
  )
}

// ============ 工具附加费(只读) ============

function ToolSurchargeSection() {
  return (
    <Section
      title="工具附加费"
      subtitle="服务端工具调用(联网搜索/文件检索/图像生成)的上游附加费,按官方默认价计费。"
    >
      <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>
        该价表无运维写端点,启停由环境变量 <code>HUAKAI_TOOL_SURCHARGE_ENABLED</code>(默认开)控制。下表为后端默认价(只读)。
      </div>
      <Card>
        <Table head={['工具', '名称', '价格(USD/1000 次)', '备注']}>
          {TOOL_SURCHARGE_DEFAULTS.map((t) => (
            <tr key={t.tool}>
              <td className="hk-mono">{t.tool}</td>
              <td>{t.label}</td>
              <td className="hk-mono" style={{ textAlign: 'right' }}>${t.perThousandUSD}</td>
              <td style={{ color: 'var(--hk-ink-500)' }}>{t.note}</td>
            </tr>
          ))}
        </Table>
      </Card>
    </Section>
  )
}

// ============ 共用小组件 ============

function GatedHint() {
  return (
    <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 12, color: 'var(--hk-warn)', background: 'var(--hk-warn-soft)', border: '1px solid var(--hk-warn-soft)' }}>
      该写动作直接修改计费倍率(money-gated),需 Owner 确认。后端要求 platform_admin 角色。
    </div>
  )
}

function Section({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) {
  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        <h2 style={{ fontSize: 17 }}>{title}</h2>
        <p style={{ margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }}>{subtitle}</p>
      </div>
      {children}
    </section>
  )
}

function Card({ children }: { children: React.ReactNode }) {
  return <div className="hk-card">{children}</div>
}

function Table({ head, children }: { head: string[]; children: React.ReactNode }) {
  return (
    <div className="hk-tablewrap">
      <table className="hk-table">
        <thead>
          <tr>
            {head.map((h, i) => (
              <th key={h || i}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  )
}

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 'min(440px,100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <h2 style={{ fontSize: 18 }}>{title}</h2>
        {children}
      </div>
    </div>
  )
}

function ModalActions({ onCancel, onConfirm, busy }: { onCancel: () => void; onConfirm: () => void; busy: boolean }) {
  return (
    <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
      <button type="button" onClick={onCancel} className="hk-btn">取消</button>
      <button type="button" disabled={busy} onClick={onConfirm} className="hk-btn hk-btn--green">
        {busy ? '提交中…' : '确认提交(需 Owner 确认)'}
      </button>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}

function ErrorBox({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>
      {children}
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

function rowKey(r: CacheOverride): string {
  return `${r.scope}:${r.model ?? ''}:${r.tenant_id ?? ''}`
}

function fmt(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const toolbar: React.CSSProperties = { display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', flexWrap: 'wrap', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }
const fieldInline: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }
