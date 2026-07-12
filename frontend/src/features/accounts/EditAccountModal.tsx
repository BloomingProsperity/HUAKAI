import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { listProxies } from '../proxies/api'
import type { Proxy } from '../proxies/types'
import { updateProviderAccount } from './api'
import { buildAccountUpdate, formFromAccount, type AccountEditForm } from './edit'
import type { ProviderAccount } from './types'

/*
 * 账号参数编辑模态(P1)。PATCH /admin/v1/provider-accounts/{id} 改池调优旋钮 + 出站/高级设置:
 * 基础(优先级 / 静态权重 / 并发上限 / 标签)+ 折叠的「高级 / 出站」分区
 * (出站代理绑定 / 探测模型 / 模型白名单 / 能力标记 / 自定义错误码 / 临时不可调度)。
 * 仅下发改动字段(buildAccountUpdate);无改动时不发请求。
 */
export function EditAccountModal({
  account,
  onClose,
  onSaved,
}: {
  account: ProviderAccount
  onClose: () => void
  onSaved: (updated: ProviderAccount) => void
}) {
  const [form, setForm] = useState<AccountEditForm>(formFromAccount(account))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // 高级分区默认收起;若账号已绑代理/已配高级项,自动展开以露出现状。
  const [showAdvanced, setShowAdvanced] = useState(
    account.proxy_id != null ||
      !!account.proxy_group_id ||
      (account.custom_error_codes_enabled ?? false) ||
      (account.temp_unschedulable_enabled ?? false),
  )
  // 出站单代理下拉的候选(仅本账号租户,secret-free)。
  const [proxies, setProxies] = useState<Proxy[]>([])
  const [proxyErr, setProxyErr] = useState<string | null>(null)
  const set = <K extends keyof AccountEditForm>(k: K, v: AccountEditForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  // 拉本租户出站代理列表供「单代理」模式下拉;失败只提示,不阻塞其它字段编辑。
  useEffect(() => {
    const ctrl = new AbortController()
    listProxies(account.tenant_id, ctrl.signal)
      .then((r) => setProxies(r.items))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setProxyErr(e instanceof ApiError ? `${e.message}(${e.code})` : '加载代理列表失败')
      })
    return () => ctrl.abort()
  }, [account.tenant_id])

  const submit = async () => {
    const built = buildAccountUpdate(account, form)
    if ('error' in built) {
      setError(built.error)
      return
    }
    if ('noop' in built) {
      onClose()
      return
    }
    setBusy(true)
    setError(null)
    try {
      const updated = await updateProviderAccount(account.id, built)
      onSaved(updated)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 'min(480px,100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <h2 style={{ fontSize: 18 }}>编辑账号参数</h2>
        <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }}>仅保存改动的字段;留空标签即清空。</p>
        <Field label="优先级(priority,越小越先选)">
          <input value={form.priority} onChange={(e) => set('priority', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="静态权重(static_weight)">
          <input value={form.staticWeight} onChange={(e) => set('staticWeight', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="并发上限(cap_concurrency)">
          <input value={form.capConcurrency} onChange={(e) => set('capConcurrency', e.target.value)} inputMode="numeric" style={inp} />
        </Field>
        <Field label="标签(逗号分隔)">
          <input value={form.tags} onChange={(e) => set('tags', e.target.value)} placeholder="prod, us, tier1" style={inp} />
        </Field>

        <button type="button" onClick={() => setShowAdvanced((s) => !s)} style={{ ...ghost, alignSelf: 'flex-start' }}>
          {showAdvanced ? '收起高级 / 出站设置' : '展开高级 / 出站设置'}
        </button>

        {showAdvanced && (
          <fieldset style={section}>
            <legend style={legend}>高级 / 出站</legend>

            <Field label="出站代理(proxy_binding)">
              <select value={form.proxyMode} onChange={(e) => set('proxyMode', e.target.value as AccountEditForm['proxyMode'])} style={inp}>
                <option value="direct">直连(不走代理)</option>
                <option value="proxy">指定单代理</option>
                <option value="group">指定代理组</option>
              </select>
            </Field>
            {form.proxyMode === 'proxy' && (
              <Field label="选择代理">
                <select value={form.proxyId} onChange={(e) => set('proxyId', e.target.value)} style={inp}>
                  <option value="">请选择代理…</option>
                  {proxies.map((p) => (
                    <option key={p.id} value={String(p.id)}>
                      {p.name}({p.protocol}://{p.host}:{p.port}) · {p.status}
                    </option>
                  ))}
                </select>
              </Field>
            )}
            {form.proxyMode === 'group' && (
              <Field label="代理组标识(proxy_group_id)">
                <input value={form.proxyGroupId} onChange={(e) => set('proxyGroupId', e.target.value)} placeholder="如 us-residential" style={inp} />
              </Field>
            )}
            {proxyErr && form.proxyMode === 'proxy' && (
              <p style={{ fontSize: 12, color: 'var(--hk-warn)', margin: 0 }}>{proxyErr}</p>
            )}

            <Field label="探测模型(probe_model,留空清空)">
              <input value={form.probeModel} onChange={(e) => set('probeModel', e.target.value)} placeholder="如 claude-3-5-haiku" style={inp} />
            </Field>
            <Field label="模型白名单(model_allow_list,逗号分隔,留空=不限)">
              <input value={form.modelAllowList} onChange={(e) => set('modelAllowList', e.target.value)} placeholder="gpt-4o, claude-3-5-sonnet" style={inp} />
            </Field>
            <Field label="能力标记(capability_flags,逗号分隔)">
              <input value={form.capabilityFlags} onChange={(e) => set('capabilityFlags', e.target.value)} placeholder="vision, tools" style={inp} />
            </Field>

            <label style={checkRow}>
              <input type="checkbox" checked={form.customErrorCodesEnabled} onChange={(e) => set('customErrorCodesEnabled', e.target.checked)} />
              启用账号级自定义错误码(custom_error_codes_enabled)
            </label>
            <Field label="自定义错误码(逗号分隔的 HTTP 状态码)">
              <input value={form.customErrorCodes} onChange={(e) => set('customErrorCodes', e.target.value)} placeholder="429, 529" style={inp} />
            </Field>

            <label style={checkRow}>
              <input type="checkbox" checked={form.tempUnschedulableEnabled} onChange={(e) => set('tempUnschedulableEnabled', e.target.checked)} />
              临时不可调度(temp_unschedulable_enabled)
            </label>
          </fieldset>
        )}

        <Field label="变更原因(可选,记入审计)">
          <input value={form.reason} onChange={(e) => set('reason', e.target.value)} style={inp} />
        </Field>
        {error && <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{error}</div>}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} style={ghost}>
            取消
          </button>
          <button type="button" disabled={busy} onClick={submit} style={primary}>
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
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

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const section: React.CSSProperties = { border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', padding: 'var(--hk-space-3)', margin: 0, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }
const legend: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-500)', padding: '0 6px' }
const checkRow: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 12, color: 'var(--hk-ink-700)' }
const primary: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghost: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
