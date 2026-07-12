import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { updateApiKey } from './api'
import { buildKeyUpdate, formFromKey, statusToggle, type ExpiryMode, type KeyEditForm } from './edit'
import { KeyControlsSection } from './KeyControlsSection'
import type { ApiKeyView } from './types'

/*
 * Key 编辑模态(P1)。PATCH /v1/api-keys/{id}:改名 + 到期三态(保持不变 / 永不过期 / 设定日期)。
 * 仅下发改动字段(buildKeyUpdate);无改动直接关闭。绝不回显明文 key(只展示 prefix)。
 */
export function EditKeyModal({
  apiKey,
  onClose,
  onSaved,
}: {
  apiKey: ApiKeyView
  onClose: () => void
  onSaved: () => void
}) {
  const [form, setForm] = useState<KeyEditForm>(formFromKey(apiKey))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // 启用/停用切换独立于改名/到期保存(走 PATCH status),单独的 busy/error。
  const [statusBusy, setStatusBusy] = useState(false)
  const toggle = statusToggle(apiKey.status)
  // 高级控制(配额/分组/IP 白黑名单/模型白名单)默认折叠,展开后逐项 GET 回填 + PUT 保存。
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const set = <K extends keyof KeyEditForm>(k: K, v: KeyEditForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    const built = buildKeyUpdate(apiKey, form)
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
      await updateApiKey(apiKey.api_key_id, built)
      onSaved()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  // 启用/停用切换:PATCH status active|revoked。成功后刷新列表并关闭。
  const doToggleStatus = async () => {
    if (!toggle) return
    if (!window.confirm(toggle.confirmMsg)) return
    setStatusBusy(true)
    setError(null)
    try {
      await updateApiKey(apiKey.api_key_id, { status: toggle.nextStatus })
      onSaved()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '状态切换失败')
    } finally {
      setStatusBusy(false)
    }
  }

  const modes: Array<{ value: ExpiryMode; label: string }> = [
    { value: 'keep', label: '保持不变' },
    { value: 'never', label: '永不过期' },
    { value: 'date', label: '设定日期' },
  ]

  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 'min(560px,100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <h2 style={{ fontSize: 18 }}>编辑 Key</h2>
        <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }}>
          <code style={{ fontFamily: 'var(--hk-font-mono)' }}>{apiKey.key_prefix}</code>
        </p>

        {/* 启用/停用切换(独立于下方改名/到期保存):停用=立即失效,重新启用=复活已撤销 key */}
        {toggle && (
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-2) var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface-sunken)' }}>
            <span style={{ fontSize: 13, color: 'var(--hk-ink-700)' }}>
              当前状态:{apiKey.status === 'active' ? '活跃' : apiKey.status === 'revoked' ? '已停用' : apiKey.status}
            </span>
            <button
              type="button"
              disabled={statusBusy}
              onClick={doToggleStatus}
              className={toggle.danger ? 'hk-btn hk-btn--danger hk-btn--sm' : 'hk-btn hk-btn--green hk-btn--sm'}
            >
              {statusBusy ? '处理中…' : toggle.actionLabel}
            </button>
          </div>
        )}

        <Field label="名称">
          <input value={form.name} onChange={(e) => set('name', e.target.value)} style={inp} />
        </Field>
        <Field label="到期">
          <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
            {modes.map((m) => (
              <label key={m.value} style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 13, cursor: 'pointer' }}>
                <input type="radio" name="expiry-mode" checked={form.expiryMode === m.value} onChange={() => set('expiryMode', m.value)} />
                {m.label}
              </label>
            ))}
          </div>
        </Field>
        {form.expiryMode === 'date' && (
          <Field label="到期时间">
            <input type="datetime-local" value={form.expiryDate} onChange={(e) => set('expiryDate', e.target.value)} style={inp} />
          </Field>
        )}
        {error && <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{error}</div>}

        {/* 高级控制:配额(计费敏感)/分组/IP 白黑名单/模型白名单。各项独立保存,与上方改名/到期互不影响。 */}
        <div style={{ borderTop: '1px solid var(--hk-line)', paddingTop: 'var(--hk-space-3)' }}>
          <button
            type="button"
            onClick={() => setAdvancedOpen((o) => !o)}
            style={{ border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, fontWeight: 600, cursor: 'pointer', padding: 0, display: 'flex', alignItems: 'center', gap: 6 }}
          >
            <span style={{ display: 'inline-block', transform: advancedOpen ? 'rotate(90deg)' : 'none', transition: 'transform .15s' }}>▶</span>
            高级控制(配额 / 分组 / IP / 模型)
          </button>
          {advancedOpen && (
            <div style={{ marginTop: 'var(--hk-space-3)' }}>
              <KeyControlsSection apiKeyId={apiKey.api_key_id} />
            </div>
          )}
        </div>

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
const primary: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghost: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
