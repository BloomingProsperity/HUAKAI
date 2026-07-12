import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getAdminUserNotify, putAdminUserNotify } from './api'
import {
  ADMIN_NOTIFY_TYPES,
  adminClearingSecrets,
  adminFormFromResponse,
  adminNotifyTypeLabel,
  buildAdminNotifyUpdate,
  validateTenantId,
  type AdminNotifyForm,
} from './usersNotify'
import type { AdminNotifyResponse } from './types'

/*
 * 通知偏好(管理员代管)卡。把 controlhttp notifyAdminHandler 的
 *   GET/PUT /v1/admin/users/{user_id}/notifications
 * 接到用户详情页,允许运维代某用户读写其低余额/告警通知渠道(邮件 / Webhook / Bark / Gotify)。
 *
 * 设计要点:
 *   - 目标租户 tenant_id:用户详情体不含租户,platform_admin 必须显式给(否则后端 400
 *     tenant_id_required,notify_handler.go:209);默认 1=单租户运营者租户,可改。
 *     改 tenant_id 会重新拉取该 (tenant,user) 的偏好。
 *   - secret 安全:webhook_secret/gotify_token 明文绝不回显,只展示「已配置」标志;
 *     明文仅在运维本次主动填写时才提交。
 *   - ⚠️ 密钥覆盖语义:后端 UpsertSettings 对 webhook_secret/gotify_token 无条件覆盖
 *     (store.go:209/213,EXCLUDED.*),留空提交会把已配置密钥清成空。已配置但本次留空时,
 *     保存前二次确认,避免静默清除(镜像用户侧 NotificationPrefsCard 已修的同款坑)。
 *   - 区别于「站内信收件箱」(那是收件,这是配置投递渠道)。
 */
export function UserNotifyPrefs({ userId }: { userId: number }) {
  // 目标租户输入(默认 1),与手动调额卡同款:platform_admin 需指明目标租户。
  const [tenantInput, setTenantInput] = useState('1')
  const [prefs, setPrefs] = useState<AdminNotifyResponse | null>(null)
  const [form, setForm] = useState<AdminNotifyForm | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)
  // reload 自增:切换 tenant / 保存成功后重新拉取。
  const [nonce, setNonce] = useState(0)

  const reload = useCallback(() => {
    setError(null)
    setFlash(null)
    setNonce((n) => n + 1)
  }, [])

  useEffect(() => {
    const tv = validateTenantId(tenantInput)
    if (!tv.ok) {
      // tenant_id 非法时不发请求,直接提示;清掉旧表单避免误改。
      setLoading(false)
      setForm(null)
      setPrefs(null)
      setError(tv.error)
      return
    }
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    getAdminUserNotify(userId, tv.tenantId, ctrl.signal)
      .then((r) => {
        if (ctrl.signal.aborted) return
        setPrefs(r)
        setForm(adminFormFromResponse(r))
      })
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setForm(null)
        setPrefs(null)
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载通知偏好失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
    // nonce 触发显式重载;tenantInput 改变也重拉。
  }, [userId, tenantInput, nonce])

  const set = <K extends keyof AdminNotifyForm>(k: K, v: AdminNotifyForm[K]) =>
    setForm((f) => (f ? { ...f, [k]: v } : f))

  const save = async () => {
    if (!form) return
    const tv = validateTenantId(tenantInput)
    if (!tv.ok) {
      setError(tv.error)
      setFlash(null)
      return
    }
    const built = buildAdminNotifyUpdate(form)
    if ('error' in built) {
      setError(built.error)
      setFlash(null)
      return
    }
    // ⚠️ 已配置但本次留空 = 后端无条件覆盖会清空密钥;清除前二次确认。
    const clearing = adminClearingSecrets(prefs, form)
    if (clearing.length > 0) {
      if (
        !window.confirm(
          `${clearing.join('、')}已配置,但本次留空。\n保存将清除它(相应渠道推送会失效)。\n要保留请取消并重新填写。确定清除?`,
        )
      ) {
        return
      }
    }
    setBusy(true)
    setError(null)
    setFlash(null)
    try {
      const r = await putAdminUserNotify(userId, built.body, tv.tenantId)
      setPrefs(r)
      setForm(adminFormFromResponse(r)) // secret 输入框随之清空(不回填)
      setFlash('已保存该用户的通知偏好')
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const t = form?.notifyType ?? 'none'

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <h2 style={{ fontSize: 15, color: 'var(--hk-ink-700)' }}>通知偏好(代管)</h2>

      <div style={card}>
        <Field label="目标租户 ID(tenant_id)" hint="用户详情不含租户,需指明;单租户运营者通常为 1。改后重新加载">
          <input
            value={tenantInput}
            onChange={(e) => setTenantInput(e.target.value)}
            inputMode="numeric"
            placeholder="如 1"
            style={{ ...inp, maxWidth: 140 }}
          />
        </Field>

        <p style={hint}>
          代该用户配置低余额与告警通知的投递渠道(区别于站内信收件箱)。平台绝不回显已存密钥;
          ⚠️ 已配置的密钥 / Token 留空保存会被清除,要保留请重新填写。
        </p>

        {error && <ErrBox>{error}</ErrBox>}
        {flash && <OkBox>{flash}</OkBox>}

        {loading && !form ? (
          <Muted>加载中…</Muted>
        ) : form ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', maxWidth: 460 }}>
            <Field label="通知渠道">
              <select value={form.notifyType} onChange={(e) => set('notifyType', e.target.value)} style={inp}>
                {ADMIN_NOTIFY_TYPES.map((n) => (
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
                <StatusBadge tone="info">{adminNotifyTypeLabel(prefs.notify_type)}</StatusBadge>
                {prefs.webhook_secret_configured && <StatusBadge tone="muted">Webhook 密钥已配置</StatusBadge>}
                {prefs.gotify_token_configured && <StatusBadge tone="muted">Gotify Token 已配置</StatusBadge>}
                {prefs.updated_by && <span>最近修改:{prefs.updated_by}</span>}
              </div>
            )}

            <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>
              <button type="button" disabled={busy} onClick={save} style={primaryBtn}>
                {busy ? '保存中…' : '保存通知偏好'}
              </button>
              <button type="button" disabled={busy || loading} onClick={reload} style={ghostBtn}>
                重新加载
              </button>
            </div>
          </div>
        ) : null}
      </div>
    </section>
  )
}

/* ---------------- 本卡自有的小组件与样式 ---------------- */

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      <span style={{ display: 'flex', alignItems: 'baseline', gap: 'var(--hk-space-2)' }}>
        <span style={{ fontWeight: 600, color: 'var(--hk-ink-700)' }}>{label}</span>
        {hint && <span>{hint}</span>}
      </span>
      {children}
    </label>
  )
}

function Muted({ children }: { children: React.ReactNode }) {
  return <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>{children}</div>
}
function ErrBox({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{children}</div>
}
function OkBox({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-primary-700)', background: 'var(--hk-primary-50, var(--hk-primary-50))', border: '1px solid var(--hk-line)' }}>{children}</div>
}

const card: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
  padding: 'var(--hk-space-4)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
}
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const hint: React.CSSProperties = { margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer', flexShrink: 0 }
