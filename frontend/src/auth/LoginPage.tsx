import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { login, loginTwoFactor, register } from './api'
import { setAdminToken, setSession, useAuth } from './store'

/*
 * 登录 / 注册页(demo 链入口)。用户态登录拿 session_token 存入 auth store;运维者可另配 admin
 * token(运维端点需要)。2FA 时进验证码步。本页在 AppShell 之外,登录后跳回控制台。
 */
type Mode = 'login' | 'register' | '2fa'

export function LoginPage() {
  const nav = useNavigate()
  const auth = useAuth()
  const [mode, setMode] = useState<Mode>('login')
  const [tenantId, setTenantId] = useState('1')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [code, setCode] = useState('')
  const [challengeId, setChallengeId] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [adminTokenDraft, setAdminTokenDraft] = useState('')

  const tid = () => Number(tenantId.trim()) || 0

  const onLogin = async () => {
    setBusy(true)
    setError(null)
    try {
      const r = await login(tid(), email.trim(), password)
      if (r.kind === '2fa') {
        setChallengeId(r.challengeId)
        setMode('2fa')
        return
      }
      setSession(r.token, r.user)
      nav('/', { replace: true })
    } catch (e) {
      setError(authErr(e))
    } finally {
      setBusy(false)
    }
  }

  const onTwoFactor = async () => {
    setBusy(true)
    setError(null)
    try {
      const r = await loginTwoFactor(challengeId, code.trim())
      setSession(r.token, r.user)
      nav('/', { replace: true })
    } catch (e) {
      setError(authErr(e))
    } finally {
      setBusy(false)
    }
  }

  const onRegister = async () => {
    setBusy(true)
    setError(null)
    try {
      await register(tid(), email.trim(), password, displayName, inviteCode)
      setNotice('注册成功,请登录(若开启邮箱验证,请先完成验证)。')
      setMode('login')
    } catch (e) {
      setError(authErr(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={page}>
      <div style={card}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', marginBottom: 'var(--hk-space-2)' }}>
          <span aria-hidden style={logo} />
          <h1 style={{ fontSize: 20, fontWeight: 700 }}>HUAKAI 控制台</h1>
        </div>

        {mode !== '2fa' && (
          <div style={{ display: 'flex', gap: 'var(--hk-space-2)', marginBottom: 'var(--hk-space-2)' }}>
            <Tab active={mode === 'login'} onClick={() => setMode('login')}>
              登录
            </Tab>
            <Tab active={mode === 'register'} onClick={() => setMode('register')}>
              注册
            </Tab>
          </div>
        )}

        {notice && <Banner tone="ok">{notice}</Banner>}
        {error && <Banner tone="danger">{error}</Banner>}

        {mode === '2fa' ? (
          <>
            <p style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>请输入两步验证码。</p>
            <Field label="验证码">
              <input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoFocus style={inp} />
            </Field>
            <button type="button" disabled={busy} onClick={onTwoFactor} style={primary}>
              {busy ? '校验中…' : '验证并登录'}
            </button>
            <button type="button" onClick={() => setMode('login')} style={linkBtn}>
              ← 返回
            </button>
          </>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              mode === 'login' ? onLogin() : onRegister()
            }}
            style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}
          >
            <Field label="租户 ID">
              <input value={tenantId} onChange={(e) => setTenantId(e.target.value)} inputMode="numeric" style={inp} />
            </Field>
            <Field label="邮箱">
              <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" style={inp} />
            </Field>
            {mode === 'register' && (
              <Field label="显示名(可选)">
                <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} style={inp} />
              </Field>
            )}
            <Field label="密码">
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete={mode === 'login' ? 'current-password' : 'new-password'} style={inp} />
            </Field>
            {mode === 'register' && (
              <Field label="邀请码(可选)">
                <input value={inviteCode} onChange={(e) => setInviteCode(e.target.value)} style={inp} />
              </Field>
            )}
            <button type="submit" disabled={busy} style={primary}>
              {busy ? '处理中…' : mode === 'login' ? '登录' : '注册'}
            </button>
          </form>
        )}

        <details style={{ marginTop: 'var(--hk-space-3)', fontSize: 12, color: 'var(--hk-ink-500)' }}>
          <summary style={{ cursor: 'pointer' }}>运维者:配置 admin token{auth.hasAdminToken ? '(已配置)' : ''}</summary>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)', marginTop: 'var(--hk-space-2)' }}>
            <p style={{ margin: 0 }}>运维端点(账号池 / 路由)用独立 admin token 鉴权。粘贴你的 admin token:</p>
            <input type="password" value={adminTokenDraft} onChange={(e) => setAdminTokenDraft(e.target.value)} placeholder="admin token" style={inp} />
            <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
              <button type="button" onClick={() => { setAdminToken(adminTokenDraft); setAdminTokenDraft(''); setNotice('admin token 已保存。') }} style={ghost}>
                保存
              </button>
              {auth.hasAdminToken && (
                <button type="button" onClick={() => setAdminToken(null)} style={ghost}>
                  清除
                </button>
              )}
            </div>
          </div>
        </details>
      </div>
    </div>
  )
}

function authErr(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.code === 'invalid_credentials' || e.status === 401) return '邮箱或密码错误'
    return `${e.message}(${e.code})`
  }
  return '请求失败,请稍后重试'
}

function Tab({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" onClick={onClick} style={{ flex: 1, height: 34, border: 'none', borderBottom: `2px solid ${active ? 'var(--hk-primary-500)' : 'transparent'}`, background: 'transparent', color: active ? 'var(--hk-primary-700)' : 'var(--hk-ink-500)', fontWeight: active ? 600 : 400, fontSize: 14, cursor: 'pointer' }}>
      {children}
    </button>
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
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: ok ? '#0b6553' : '#8f322a', background: ok ? 'var(--hk-primary-50)' : '#fbe9e7', border: `1px solid ${ok ? 'var(--hk-primary-100)' : '#f2cdc8'}` }}>{children}</div>
}

const page: React.CSSProperties = { minHeight: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--hk-canvas)', padding: 'var(--hk-space-5)' }
const card: React.CSSProperties = { width: 'min(400px, 100%)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-2)', padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }
const logo: React.CSSProperties = { width: 24, height: 24, borderRadius: 'var(--hk-radius-sm)', background: 'linear-gradient(135deg, var(--hk-primary-500), var(--hk-primary-700))', display: 'inline-block' }
const inp: React.CSSProperties = { height: 34, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const primary: React.CSSProperties = { height: 38, marginTop: 'var(--hk-space-2)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 14, fontWeight: 600, cursor: 'pointer' }
const ghost: React.CSSProperties = { height: 30, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 12, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { alignSelf: 'flex-start', border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: 0 }
