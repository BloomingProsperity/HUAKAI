import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getNotifyPrefs, updateNotifyPrefs } from './notifyPrefsApi'
import {
  buildNotifyUpdate,
  formFromResponse,
  notifyTypeLabel,
  NOTIFY_TYPES,
  type NotifyPrefsForm,
} from './notifyPrefs'
import type { NotifyPrefsResponse } from './notifyPrefsTypes'

/*
 * 通知偏好卡(user 壳,挂在个人资料页)。自助管理低余额/告警通知渠道:邮件 / Webhook / Bark / Gotify。
 * 区别于「站内信收件箱」(那是收件,这是配置投递渠道)。
 * 安全:secret/token 明文绝不回显,只展示「已配置」标志;明文仅在用户主动填写时才提交。
 * 端点 /v1/users/me/notifications(session 鉴权,身份后端从会话派生)。
 */
export function NotificationPrefsCard() {
  const [prefs, setPrefs] = useState<NotifyPrefsResponse | null>(null)
  const [form, setForm] = useState<NotifyPrefsForm | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    getNotifyPrefs(signal)
      .then((r) => {
        setPrefs(r)
        setForm(formFromResponse(r))
      })
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载通知偏好失败')
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const set = <K extends keyof NotifyPrefsForm>(k: K, v: NotifyPrefsForm[K]) =>
    setForm((f) => (f ? { ...f, [k]: v } : f))

  const save = async () => {
    if (!form) return
    const built = buildNotifyUpdate(form)
    if ('error' in built) {
      setError(built.error)
      setFlash(null)
      return
    }
    // ⚠️ 后端 UpsertSettings 对 webhook_secret/gotify_token 是无条件覆盖(store.go:209/213,
    // EXCLUDED.*),留空提交会把已配置密钥清成空。已配置但本次留空时,二次确认避免静默清除。
    const clearingWebhook = !!prefs?.webhook_secret_configured && form.webhookSecret.trim() === ''
    const clearingGotify = !!prefs?.gotify_token_configured && form.gotifyToken.trim() === ''
    if (clearingWebhook || clearingGotify) {
      const which = [clearingWebhook ? 'Webhook 密钥' : '', clearingGotify ? 'Gotify Token' : ''].filter(Boolean).join('、')
      if (!window.confirm(`${which}已配置但本次留空,保存将清除它(相应渠道推送会失效)。要保留请返回重填。确定清除?`)) return
    }
    setBusy(true)
    setError(null)
    setFlash(null)
    try {
      const r = await updateNotifyPrefs(built.body)
      setPrefs(r)
      setForm(formFromResponse(r)) // secret 输入框随之清空(不回填)
      setFlash('通知偏好已保存')
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const t = form?.notifyType ?? 'none'

  return (
    <Card title="通知偏好">
      <p style={hint}>
        配置低余额与告警通知的投递渠道(区别于站内信收件箱)。平台绝不回显已存密钥;⚠️ 已配置的密钥/Token 留空保存会被清除,要保留请重新填写。
      </p>
      {error && <ErrBox>{error}</ErrBox>}
      {flash && <OkBox>{flash}</OkBox>}
      {loading && !form ? (
        <Muted>加载中…</Muted>
      ) : form ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', maxWidth: 460 }}>
          <Field label="通知渠道">
            <select value={form.notifyType} onChange={(e) => set('notifyType', e.target.value)} style={inp}>
              {NOTIFY_TYPES.map((n) => (
                <option key={n.value} value={n.value}>
                  {n.label}
                </option>
              ))}
            </select>
          </Field>

          {/* 邮件渠道 */}
          {(t === 'email' || t === 'none') && (
            <Field label="通知邮箱(邮件渠道)">
              <input value={form.notificationEmail} onChange={(e) => set('notificationEmail', e.target.value)} placeholder="alerts@example.com" style={inp} />
            </Field>
          )}

          {/* Webhook 渠道 */}
          {t === 'webhook' && (
            <>
              <Field label="Webhook URL">
                <input value={form.webhookURL} onChange={(e) => set('webhookURL', e.target.value)} placeholder="https://…" style={inp} />
              </Field>
              <Field label={`Webhook 密钥${prefs?.webhook_secret_configured ? '(已配置,留空将清除!要保留请重填)' : ''}`}>
                <input type="password" autoComplete="new-password" value={form.webhookSecret} onChange={(e) => set('webhookSecret', e.target.value)} placeholder={prefs?.webhook_secret_configured ? '••••••(已配置)' : '可选'} style={inp} />
              </Field>
            </>
          )}

          {/* Bark 渠道 */}
          {t === 'bark' && (
            <Field label="Bark URL">
              <input value={form.barkURL} onChange={(e) => set('barkURL', e.target.value)} placeholder="https://api.day.app/…" style={inp} />
            </Field>
          )}

          {/* Gotify 渠道 */}
          {t === 'gotify' && (
            <>
              <Field label="Gotify URL">
                <input value={form.gotifyURL} onChange={(e) => set('gotifyURL', e.target.value)} placeholder="https://gotify.example.com" style={inp} />
              </Field>
              <Field label={`Gotify Token${prefs?.gotify_token_configured ? '(已配置,留空将清除!要保留请重填)' : ''}`}>
                <input type="password" autoComplete="new-password" value={form.gotifyToken} onChange={(e) => set('gotifyToken', e.target.value)} placeholder={prefs?.gotify_token_configured ? '••••••(已配置)' : '可选'} style={inp} />
              </Field>
              <Field label="Gotify 优先级(可选)">
                <input value={form.gotifyPriority} onChange={(e) => set('gotifyPriority', e.target.value)} placeholder="默认 5" inputMode="numeric" style={{ ...inp, width: 120 }} />
              </Field>
            </>
          )}

          <Field label="低余额告警阈值(USD,可选)">
            <input value={form.balanceThreshold} onChange={(e) => set('balanceThreshold', e.target.value)} placeholder="低于该余额触发告警" inputMode="decimal" style={{ ...inp, width: 200 }} />
          </Field>

          <Field label="额外抄送邮箱(每行一个,最多 10 条)">
            <textarea value={form.extraEmailsText} onChange={(e) => set('extraEmailsText', e.target.value)} placeholder={'cc1@example.com\ncc2@example.com'} rows={3} style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', resize: 'vertical' }} />
          </Field>

          {prefs && (
            <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', flexWrap: 'wrap', fontSize: 12, color: 'var(--hk-ink-500)' }}>
              <span>当前渠道:</span>
              <StatusBadge tone="info">{notifyTypeLabel(prefs.notify_type)}</StatusBadge>
              {prefs.webhook_secret_configured && <StatusBadge tone="muted">Webhook 密钥已配置</StatusBadge>}
              {prefs.gotify_token_configured && <StatusBadge tone="muted">Gotify Token 已配置</StatusBadge>}
            </div>
          )}

          <div>
            <button type="button" disabled={busy} onClick={save} style={primaryBtn}>
              {busy ? '保存中…' : '保存通知偏好'}
            </button>
          </div>
        </div>
      ) : null}
    </Card>
  )
}

/* ---------------- 本卡自有的小组件与样式(不依赖 ProfilePage 内部 helper) ---------------- */

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section
      style={{
        background: 'var(--hk-surface)',
        border: '1px solid var(--hk-line)',
        borderRadius: 'var(--hk-radius-lg)',
        boxShadow: 'var(--hk-shadow-1)',
        padding: 'var(--hk-space-5)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--hk-space-3)',
      }}
    >
      <h2 style={{ fontSize: 16, margin: 0, color: 'var(--hk-ink-900)' }}>{title}</h2>
      {children}
    </section>
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

function Muted({ children }: { children: React.ReactNode }) {
  return <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>{children}</div>
}
function ErrBox({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{children}</div>
}
function OkBox({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }}>{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const hint: React.CSSProperties = { margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
