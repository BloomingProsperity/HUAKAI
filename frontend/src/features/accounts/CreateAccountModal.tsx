import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { createProviderAccount, fetchAccountModes, fetchChannels, fetchProviders } from './createApi'
import {
  ACCOUNT_TYPE_OPTIONS,
  buildCreateRequest,
  EMPTY_CREATE_FORM,
  modeKey,
  validateCreateForm,
  type CreateAccountForm,
} from './create'
import type { AccountMode, ChannelCatalogItem, ProviderCatalogItem } from './createTypes'

/*
 * 新建账号向导(单模态)。account-modes 元数据驱动:选模式 → 据 required_fields 渲染凭据输入
 * (secret 字段密文输入、不回显)→ 提交;混合渠道风险时弹二次确认(confirm=true 复发)。
 * 成功后回调刷新列表。
 */
export function CreateAccountModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [providers, setProviders] = useState<ProviderCatalogItem[]>([])
  const [channels, setChannels] = useState<ChannelCatalogItem[]>([])
  const [modes, setModes] = useState<AccountMode[]>([])
  const [loadErr, setLoadErr] = useState<string | null>(null)

  const [form, setForm] = useState<CreateAccountForm>(EMPTY_CREATE_FORM)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [mixedRisk, setMixedRisk] = useState<{ message: string; risks: unknown[] } | null>(null)
  const [showAdvanced, setShowAdvanced] = useState(false)

  useEffect(() => {
    const ctrl = new AbortController()
    Promise.all([fetchProviders(ctrl.signal), fetchChannels(ctrl.signal), fetchAccountModes(ctrl.signal)])
      .then(([p, c, m]) => {
        setProviders(p.items.filter((x) => x.enabled))
        setChannels(c.items.filter((x) => x.enabled))
        setModes(m.modes.filter((x) => x.is_enabled))
      })
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setLoadErr(e instanceof ApiError ? `${e.message}(${e.code})` : '加载目录失败')
      })
    return () => ctrl.abort()
  }, [])

  const selectedMode = useMemo(
    () => modes.find((m) => modeKey(m) === form.modeKey) ?? null,
    [modes, form.modeKey],
  )

  const set = <K extends keyof CreateAccountForm>(k: K, v: CreateAccountForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  const setCred = (name: string, v: string) =>
    setForm((f) => ({ ...f, credentialValues: { ...f.credentialValues, [name]: v } }))

  const submit = async (confirm: boolean) => {
    const invalid = validateCreateForm(form, selectedMode)
    if (invalid) {
      setError(invalid)
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      const result = await createProviderAccount(buildCreateRequest(form, selectedMode, confirm))
      if ('mixedRisk' in result) {
        setMixedRisk({ message: result.message, risks: result.risks })
        return
      }
      onCreated()
      onClose()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '创建账号失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Overlay onClose={onClose}>
      <div style={panel} onClick={(e) => e.stopPropagation()}>
        <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <h2 style={{ fontSize: 18 }}>新建上游账号</h2>
          <button type="button" onClick={onClose} style={iconBtn} aria-label="关闭">
            ✕
          </button>
        </header>

        {loadErr && <Banner tone="danger">{loadErr}</Banner>}

        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)', overflowY: 'auto' }}>
          <Section title="基本">
            <Field label="上游 provider">
              <select value={form.providerId} onChange={(e) => set('providerId', e.target.value)} style={inp}>
                <option value="">请选择…</option>
                {providers.map((p) => (
                  <option key={p.id} value={String(p.id)}>
                    {p.display_name}({p.code})
                  </option>
                ))}
              </select>
            </Field>
            <Field label="渠道 channel">
              <select value={form.channelId} onChange={(e) => set('channelId', e.target.value)} style={inp}>
                <option value="">请选择…</option>
                {channels.map((c) => (
                  <option key={c.id} value={String(c.id)}>
                    {c.name}(#{c.id})
                  </option>
                ))}
              </select>
            </Field>
            <Field label="账号名称">
              <input value={form.name} onChange={(e) => set('name', e.target.value)} placeholder="如 claude-主号-01" style={inp} />
            </Field>
            <Field label="账号类型">
              <select value={form.accountType} onChange={(e) => set('accountType', e.target.value)} style={inp}>
                <option value="">请选择…</option>
                {ACCOUNT_TYPE_OPTIONS.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </Field>
          </Section>

          <Section title="凭据模式(account-modes 驱动)">
            <Field label="vendor × auth_mode">
              <select value={form.modeKey} onChange={(e) => set('modeKey', e.target.value)} style={inp}>
                <option value="">请选择凭据模式…</option>
                {modes.map((m) => (
                  <option key={modeKey(m)} value={modeKey(m)}>
                    {m.vendor} · {m.auth_mode}
                    {m.is_experimental ? '(实验)' : ''}
                  </option>
                ))}
              </select>
            </Field>
            {selectedMode && (
              <div style={{ display: 'flex', gap: 'var(--hk-space-2)', flexWrap: 'wrap', alignItems: 'center' }}>
                <StatusBadge tone={riskTone(selectedMode.risk_level)}>风险:{selectedMode.risk_level || '—'}</StatusBadge>
                {selectedMode.is_experimental && <StatusBadge tone="warn">实验性</StatusBadge>}
                {selectedMode.manual_first && <StatusBadge tone="muted">Manual-First</StatusBadge>}
                {selectedMode.risk_reasons.map((r, i) => (
                  <span key={i} style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>
                    {r}
                  </span>
                ))}
              </div>
            )}
          </Section>

          {selectedMode && selectedMode.required_fields.length > 0 && (
            <Section title="凭据字段">
              {selectedMode.required_fields.map((f) => (
                <Field
                  key={f.name}
                  label={`${f.name}${f.required ? '' : '(可选)'}${f.one_of_group ? ` · 任一(${f.one_of_group})` : ''}`}
                >
                  {f.kind === 'textarea' || f.kind === 'json_object' ? (
                    <textarea
                      value={form.credentialValues[f.name] ?? ''}
                      onChange={(e) => setCred(f.name, e.target.value)}
                      rows={3}
                      placeholder={f.input || (f.kind === 'json_object' ? '{ ... }' : '')}
                      style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', fontFamily: 'var(--hk-font-mono)' }}
                    />
                  ) : (
                    <input
                      type={f.redaction === 'secret' ? 'password' : 'text'}
                      autoComplete="off"
                      value={form.credentialValues[f.name] ?? ''}
                      onChange={(e) => setCred(f.name, e.target.value)}
                      placeholder={f.input || ''}
                      style={inp}
                    />
                  )}
                </Field>
              ))}
            </Section>
          )}

          <button type="button" onClick={() => setShowAdvanced((s) => !s)} style={{ ...ghost, alignSelf: 'flex-start' }}>
            {showAdvanced ? '收起调度参数' : '展开调度参数(可选)'}
          </button>
          {showAdvanced && (
            <Section title="调度参数(留空用后端默认)">
              <Field label="优先级">
                <input value={form.priority} onChange={(e) => set('priority', e.target.value)} inputMode="numeric" style={inp} />
              </Field>
              <Field label="静态权重">
                <input value={form.staticWeight} onChange={(e) => set('staticWeight', e.target.value)} inputMode="numeric" style={inp} />
              </Field>
              <Field label="并发上限">
                <input value={form.capConcurrency} onChange={(e) => set('capConcurrency', e.target.value)} inputMode="numeric" style={inp} />
              </Field>
              <Field label="探测模型">
                <input value={form.probeModel} onChange={(e) => set('probeModel', e.target.value)} style={inp} />
              </Field>
              <Field label="标签(逗号/空格分隔)">
                <input value={form.tags} onChange={(e) => set('tags', e.target.value)} style={inp} />
              </Field>
            </Section>
          )}

          <Field label="审计原因(可选)">
            <input value={form.reason} onChange={(e) => set('reason', e.target.value)} placeholder="记入 admin 审计" style={inp} />
          </Field>
        </div>

        {error && <Banner tone="danger">{error}</Banner>}

        {mixedRisk ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
            <Banner tone="warn">{mixedRisk.message}</Banner>
            <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
              <button type="button" onClick={onClose} style={ghost}>
                取消
              </button>
              <button type="button" disabled={submitting} onClick={() => submit(true)} style={dangerBtn}>
                已审阅,确认创建
              </button>
            </div>
          </div>
        ) : (
          <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
            <button type="button" onClick={onClose} style={ghost}>
              取消
            </button>
            <button type="button" disabled={submitting} onClick={() => submit(false)} style={primary}>
              {submitting ? '创建中…' : '创建账号'}
            </button>
          </div>
        )}
      </div>
    </Overlay>
  )
}

function riskTone(level: string): 'ok' | 'warn' | 'danger' | 'muted' {
  switch (level) {
    case 'low':
      return 'ok'
    case 'medium':
      return 'warn'
    case 'high':
      return 'danger'
    default:
      return 'muted'
  }
}

function Overlay({ children, onClose }: { children: React.ReactNode; onClose: () => void }) {
  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(28,38,34,0.4)',
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'center',
        padding: 'var(--hk-space-6)',
        zIndex: 'var(--hk-z-overlay)' as unknown as number,
        overflowY: 'auto',
      }}
    >
      {children}
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <fieldset style={{ border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', padding: 'var(--hk-space-3)', margin: 0, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
      <legend style={{ fontSize: 12, color: 'var(--hk-ink-500)', padding: '0 6px' }}>{title}</legend>
      {children}
    </fieldset>
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

function Banner({ tone, children }: { tone: 'danger' | 'warn'; children: React.ReactNode }) {
  const danger = tone === 'danger'
  return (
    <div
      style={{
        padding: 'var(--hk-space-2) var(--hk-space-3)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: danger ? '#8f322a' : '#8a5e0f',
        background: danger ? '#fbe9e7' : '#fbf3df',
        border: `1px solid ${danger ? '#f2cdc8' : '#f0e2bd'}`,
      }}
    >
      {children}
    </div>
  )
}

const panel: React.CSSProperties = {
  width: 'min(640px, 100%)',
  maxHeight: '90vh',
  background: 'var(--hk-surface)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-3)',
  padding: 'var(--hk-space-5)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-4)',
}
const inp: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  width: '100%',
}
const baseBtn: React.CSSProperties = { height: 34, padding: '0 var(--hk-space-4)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const primary: React.CSSProperties = { ...baseBtn, border: '1px solid var(--hk-primary-600)', background: 'var(--hk-primary-500)', color: '#fff' }
const dangerBtn: React.CSSProperties = { ...baseBtn, border: '1px solid #c0463a', background: '#c0463a', color: '#fff' }
const ghost: React.CSSProperties = { ...baseBtn, border: '1px solid var(--hk-line)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontWeight: 400 }
const iconBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-ink-500)', fontSize: 16, cursor: 'pointer' }
