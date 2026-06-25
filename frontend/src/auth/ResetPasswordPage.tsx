import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { submitPasswordReset } from './resetPasswordApi'
import {
  MIN_PASSWORD_LENGTH,
  checkNewPassword,
  hasResetToken,
  isTokenInvalidError,
  parseResetLink,
  passwordCheckMessage,
} from './resetPassword'

/*
 * 重置密码页(public 壳,AppShell 之外)。从 URL 读 email + token(+ 可选 tenant_id):
 *  - 缺 token → 链接无效态;
 *  - 有 token → 让用户输入新密码 + 确认(可见性切换、长度 ≥8、两次一致),提交到
 *    POST /v1/auth/reset-password;
 *  - 后端回 auth_token_invalid(token 失效/已用)→ 切到专门的「链接已失效」文案;
 *  - 成功 → 成功态 + 去登录。
 * 视觉沿用玉青·克制(全部 var(--hk-*)),与登录页同壳。
 */
export function ResetPasswordPage() {
  const nav = useNavigate()
  // 链接参数在首帧解析一次即可(URL 不会在本页内变更)。
  const params = useMemo(() => parseResetLink(window.location.search), [])
  const linkValid = hasResetToken(params)

  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const [tokenDead, setTokenDead] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const check = checkNewPassword(password, confirm)
  // 仅在用户已开始输入确认框后才暴露「不一致/太短」红字,避免输入途中骚扰。
  const liveMessage = confirm.length > 0 || password.length >= MIN_PASSWORD_LENGTH ? passwordCheckMessage(check) : null

  const onSubmit = async () => {
    if (!check.ok || busy) return
    setBusy(true)
    setError(null)
    try {
      await submitPasswordReset(params, password)
      setDone(true)
    } catch (e) {
      if (e instanceof ApiError && isTokenInvalidError(e.code, e.status)) {
        // token 失效是不可恢复态:切到专门文案并隐藏表单,引导重新发起重置。
        setTokenDead(true)
        return
      }
      setError(resetErr(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={page}>
      <div style={card}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', marginBottom: 'var(--hk-space-2)' }}>
          <span aria-hidden style={logo} />
          <h1 style={{ fontSize: 20, fontWeight: 700 }}>重置密码</h1>
        </div>

        {!linkValid ? (
          <InvalidLink
            title="重置链接无效"
            body="链接缺少必要的重置参数。请重新发起「忘记密码」获取新链接。"
            onGoLogin={() => nav('/login', { replace: true })}
          />
        ) : tokenDead ? (
          <InvalidLink
            title="链接已失效"
            body="该重置链接已过期或已被使用。请重新发起「忘记密码」获取新链接。"
            onGoLogin={() => nav('/login', { replace: true })}
          />
        ) : done ? (
          <>
            <Banner tone="ok">密码已重置成功,原有登录会话已全部退出。请用新密码登录。</Banner>
            <button type="button" onClick={() => nav('/login', { replace: true })} style={primary}>
              去登录
            </button>
          </>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              onSubmit()
            }}
            style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}
          >
            {params.email && (
              <p style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>
                为账号 <strong style={{ color: 'var(--hk-ink-900)' }}>{params.email}</strong> 设置新密码。
              </p>
            )}
            {error && <Banner tone="danger">{error}</Banner>}

            <Field label={`新密码(至少 ${MIN_PASSWORD_LENGTH} 个字符)`}>
              <input
                type={reveal ? 'text' : 'password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                autoFocus
                style={inp}
              />
            </Field>
            <Field label="确认新密码">
              <input
                type={reveal ? 'text' : 'password'}
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                autoComplete="new-password"
                style={inp}
              />
            </Field>

            <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--hk-ink-500)', cursor: 'pointer' }}>
              <input type="checkbox" checked={reveal} onChange={(e) => setReveal(e.target.checked)} />
              显示密码
            </label>

            {liveMessage && <p style={{ margin: 0, fontSize: 12, color: '#8f322a' }}>{liveMessage}</p>}

            <button type="submit" disabled={busy || !check.ok} style={primary}>
              {busy ? '提交中…' : '设置新密码'}
            </button>
            <button type="button" onClick={() => nav('/login', { replace: true })} style={linkBtn}>
              返回登录
            </button>
          </form>
        )}
      </div>
    </div>
  )
}

/** 把后端错误归一成中文文案(token 失效已在调用处单独处理,这里只兜底其余情况)。 */
function resetErr(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.code === 'invalid_auth_request') return '请求无效,请确认链接完整后重试'
    return `${e.message}(${e.code})`
  }
  return '请求失败,请稍后重试'
}

function InvalidLink({ title, body, onGoLogin }: { title: string; body: string; onGoLogin: () => void }) {
  return (
    <>
      <Banner tone="danger">{title}</Banner>
      <p style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>{body}</p>
      <button type="button" onClick={onGoLogin} style={primary}>
        去登录
      </button>
    </>
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

function Banner({ tone, children }: { tone: 'ok' | 'danger'; children: React.ReactNode }) {
  const ok = tone === 'ok'
  return (
    <div
      style={{
        padding: 'var(--hk-space-2) var(--hk-space-3)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: ok ? '#0b6553' : '#8f322a',
        background: ok ? 'var(--hk-primary-50)' : '#fbe9e7',
        border: `1px solid ${ok ? 'var(--hk-primary-100)' : '#f2cdc8'}`,
      }}
    >
      {children}
    </div>
  )
}

const page: React.CSSProperties = { minHeight: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--hk-canvas)', padding: 'var(--hk-space-5)' }
const card: React.CSSProperties = { width: 'min(400px, 100%)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-2)', padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }
const logo: React.CSSProperties = { width: 24, height: 24, borderRadius: 'var(--hk-radius-sm)', background: 'linear-gradient(135deg, var(--hk-primary-500), var(--hk-primary-700))', display: 'inline-block' }
const inp: React.CSSProperties = { height: 34, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const primary: React.CSSProperties = { height: 38, marginTop: 'var(--hk-space-2)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 14, fontWeight: 600, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { alignSelf: 'flex-start', border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: 0 }
