import { useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { validateForgotForm } from './forgot'
import { requestPasswordResetEmail } from './forgotApi'

/*
 * 忘记密码页(public 壳,登录页之外)。
 *
 * 流程:输入邮箱→ 前置校验 → POST /v1/auth/reset-password(无 token 分支发重置邮件)
 *      → 成功切「邮件已发送」确认态 → 提供返回登录链接。
 *
 * 不泄露:无论邮箱是否存在,后端均回 202,前端统一显示「若该邮箱已注册,我们已发送重置邮件」,
 *        不据响应区分存在性。0 console、不打印邮箱/token。
 *
 * captcha:reset-password 端点当前未接 captcha;此处预留开关与占位位
 *         (VITE_HUAKAI_CAPTCHA_ENABLED),开启时显示占位说明,token 通过纯逻辑预留字段透传。
 */

// 公开页 captcha 开关(运维构建期注入);缺省关闭。开启仅作占位,真正的 captcha 组件待接入。
const CAPTCHA_ENABLED =
  String(import.meta.env?.VITE_HUAKAI_CAPTCHA_ENABLED ?? '').toLowerCase() === 'true'

export function ForgotPasswordPage() {
  // 单实例:租户固定为 1(不暴露给用户);状态保留供校验与重置邮件请求内部使用。
  const [tenantId] = useState('1')
  const [email, setEmail] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [sent, setSent] = useState(false)

  const onSubmit = async () => {
    setError(null)
    const invalid = validateForgotForm(email, tenantId)
    if (invalid) {
      setError(invalid)
      return
    }
    setBusy(true)
    try {
      // captcha 预留:开关开启时此处应替换为真实 captcha token;当前传 undefined。
      await requestPasswordResetEmail(email, tenantId, undefined)
      setSent(true)
    } catch (e) {
      setError(forgotErr(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={page}>
      <div style={card}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', marginBottom: 'var(--hk-space-2)' }}>
          <span aria-hidden style={logo} />
          <h1 style={{ fontSize: 20, fontWeight: 700 }}>找回密码</h1>
        </div>

        {sent ? (
          <>
            <Banner tone="ok">若该邮箱已注册,我们已向其发送密码重置邮件。请查收(含垃圾箱),按邮件指引重设密码。</Banner>
            <p style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>
              没收到?稍候片刻或确认邮箱无误后可
              <button type="button" onClick={() => setSent(false)} style={linkInline}>
                重新发送
              </button>
              。
            </p>
            <Link to="/login" style={linkBtn}>
              ← 返回登录
            </Link>
          </>
        ) : (
          <>
            <p style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>
              输入注册邮箱,我们会向其发送一封密码重置邮件。
            </p>

            {error && <Banner tone="danger">{error}</Banner>}

            <form
              onSubmit={(e) => {
                e.preventDefault()
                onSubmit()
              }}
              style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}
            >
              <Field label="邮箱">
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  autoComplete="email"
                  autoFocus
                  style={inp}
                />
              </Field>

              {CAPTCHA_ENABLED && (
                // captcha 占位:开关开启时此处接入真实人机验证组件,验证 token 经 forgotApi 透传。
                <div style={captchaSlot} aria-label="人机验证占位">
                  人机验证(待接入)
                </div>
              )}

              <button type="submit" disabled={busy} style={primary}>
                {busy ? '发送中…' : '发送重置邮件'}
              </button>
            </form>

            <Link to="/login" style={linkBtn}>
              ← 返回登录
            </Link>
          </>
        )}
      </div>
    </div>
  )
}

// 错误归一:网关 {error:{code,message}} 形态 → 中文文案。不暴露邮箱存在性。
function forgotErr(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.code === 'captcha_required') return '人机验证未通过,请重试'
    if (e.status === 429 || e.code === 'rate_limited') return '请求过于频繁,请稍后再试'
    return `${e.message}(${e.code})`
  }
  return '请求失败,请稍后重试'
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}

function Banner({ tone, children }: { tone: 'ok' | 'danger'; children: React.ReactNode }) {
  const ok = tone === 'ok'
  return (
    <div
      style={{
        padding: 'var(--hk-space-2) var(--hk-space-3)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: ok ? 'var(--hk-primary-600)' : 'var(--hk-danger)',
        background: ok ? 'var(--hk-primary-50)' : 'var(--hk-danger-soft)',
        border: `1px solid ${ok ? 'var(--hk-primary-100)' : 'var(--hk-danger-soft)'}`,
      }}
    >
      {children}
    </div>
  )
}

const page: React.CSSProperties = {
  minHeight: '100%',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  background: 'var(--hk-canvas)',
  padding: 'var(--hk-space-5)',
}
const card: React.CSSProperties = {
  width: 'min(400px, 100%)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-2)',
  padding: 'var(--hk-space-6)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-2)',
}
const logo: React.CSSProperties = {
  width: 24,
  height: 24,
  borderRadius: 'var(--hk-radius-sm)',
  background: 'linear-gradient(135deg, var(--hk-primary-500), var(--hk-primary-700))',
  display: 'inline-block',
}
const inp: React.CSSProperties = {
  height: 34,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  width: '100%',
}
const primary: React.CSSProperties = {
  height: 38,
  marginTop: 'var(--hk-space-2)',
  border: '1px solid var(--hk-primary-600)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-primary-500)',
  color: '#fff',
  fontSize: 14,
  fontWeight: 600,
  cursor: 'pointer',
}
const linkBtn: React.CSSProperties = {
  alignSelf: 'flex-start',
  border: 'none',
  background: 'transparent',
  color: 'var(--hk-primary-700)',
  fontSize: 13,
  cursor: 'pointer',
  padding: 0,
  textDecoration: 'none',
}
const linkInline: React.CSSProperties = {
  border: 'none',
  background: 'transparent',
  color: 'var(--hk-primary-700)',
  fontSize: 13,
  cursor: 'pointer',
  padding: '0 2px',
}
const captchaSlot: React.CSSProperties = {
  height: 64,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  border: '1px dashed var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface-sunken)',
  color: 'var(--hk-ink-500)',
  fontSize: 12,
}
