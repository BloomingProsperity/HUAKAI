import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { createApiKey } from './api'
import { KeyIntegrations } from './KeyIntegrations'
import {
  buildCreateKeyRequest,
  EMPTY_KEY_FORM,
  EXPIRY_PRESETS,
  KEY_ENVIRONMENTS,
  validateCreateKeyForm,
  type CreateKeyForm,
} from './create'
import type { CreateKeyResponse } from './types'

/*
 * 创建 API Key 模态。提交成功后切到"一次性明文"视图:明文仅此一次展示,提供复制 + 强提醒,
 * 关闭后后端只回 key_prefix。secret-mask:明文只在内存与本视图展示,不写日志、不持久化。
 */
export function CreateKeyModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState<CreateKeyForm>(EMPTY_KEY_FORM)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<CreateKeyResponse | null>(null)
  const [copied, setCopied] = useState(false)

  const set = <K extends keyof CreateKeyForm>(k: K, v: CreateKeyForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    const now = new Date()
    const invalid = validateCreateKeyForm(form, now)
    if (invalid) {
      setError(invalid)
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      const resp = await createApiKey(buildCreateKeyRequest(form, now))
      setCreated(resp)
      onCreated() // 列表后台刷新;明文视图仍停留供保存。
    } catch (e) {
      if (e instanceof ApiError && e.code === 'active_key_cap_reached') {
        setError('已达活跃 Key 上限,请先撤销不用的 Key 再新建')
      } else {
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '创建 Key 失败')
      }
    } finally {
      setSubmitting(false)
    }
  }

  const copy = async () => {
    if (!created) return
    try {
      await navigator.clipboard.writeText(created.plaintext)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }

  return (
    <Overlay onClose={created ? undefined : onClose}>
      <div style={panel} onClick={(e) => e.stopPropagation()}>
        {created ? (
          <>
            <h2 style={{ fontSize: 18 }}>Key 已创建</h2>
            <Banner tone="warn">{created.notice || '明文仅此一次展示,请立即复制保存;关闭后只能看到前缀。'}</Banner>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>明文密钥</span>
              <div style={plaintextBox}>
                <code style={{ wordBreak: 'break-all', fontSize: 13 }}>{created.plaintext}</code>
              </div>
            </div>
            <div style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
              前缀 <code>{created.key_prefix}</code> · 状态 {created.status}
            </div>
            <KeyIntegrations plaintext={created.plaintext} keyName={form.name} />
            <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
              <button type="button" onClick={copy} style={ghost}>
                {copied ? '已复制' : '复制明文'}
              </button>
              <button type="button" onClick={onClose} style={primary}>
                我已保存,关闭
              </button>
            </div>
          </>
        ) : (
          <>
            <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <h2 style={{ fontSize: 18 }}>新建 API Key</h2>
              <button type="button" onClick={onClose} style={iconBtn} aria-label="关闭">
                ✕
              </button>
            </header>
            <Field label="名称">
              <input value={form.name} onChange={(e) => set('name', e.target.value)} placeholder="如 生产环境-主key" maxLength={128} style={inp} />
            </Field>
            <Field label="环境">
              <select value={form.environment} onChange={(e) => set('environment', e.target.value as CreateKeyForm['environment'])} style={inp}>
                {KEY_ENVIRONMENTS.map((env) => (
                  <option key={env} value={env}>
                    {env === 'live' ? 'live(生产)' : 'test(测试)'}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="过期">
              <select value={form.expiryPreset} onChange={(e) => set('expiryPreset', e.target.value as CreateKeyForm['expiryPreset'])} style={inp}>
                {EXPIRY_PRESETS.map((p) => (
                  <option key={p.value} value={p.value}>
                    {p.label}
                  </option>
                ))}
              </select>
            </Field>
            {form.expiryPreset === 'custom' && (
              <Field label="自定义过期日期">
                <input type="date" value={form.customDate} onChange={(e) => set('customDate', e.target.value)} style={inp} />
              </Field>
            )}
            {error && <Banner tone="danger">{error}</Banner>}
            <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
              <button type="button" onClick={onClose} style={ghost}>
                取消
              </button>
              <button type="button" disabled={submitting} onClick={submit} style={primary}>
                {submitting ? '创建中…' : '创建 Key'}
              </button>
            </div>
          </>
        )}
      </div>
    </Overlay>
  )
}

function Overlay({ children, onClose }: { children: React.ReactNode; onClose?: () => void }) {
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
        color: danger ? 'var(--hk-danger)' : 'var(--hk-warn)',
        background: danger ? 'var(--hk-danger-soft)' : 'var(--hk-warn-soft)',
        border: `1px solid ${danger ? 'var(--hk-danger-soft)' : 'var(--hk-warn-soft)'}`,
      }}
    >
      {children}
    </div>
  )
}

const panel: React.CSSProperties = {
  width: 'min(520px, 100%)',
  background: 'var(--hk-surface)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-3)',
  padding: 'var(--hk-space-5)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
}
const plaintextBox: React.CSSProperties = {
  padding: 'var(--hk-space-3)',
  background: 'var(--hk-ink-900)',
  color: '#d9f2e8',
  borderRadius: 'var(--hk-radius-md)',
  fontFamily: 'var(--hk-font-mono)',
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
const ghost: React.CSSProperties = { ...baseBtn, border: '1px solid var(--hk-line)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontWeight: 400 }
const iconBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-ink-500)', fontSize: 16, cursor: 'pointer' }
